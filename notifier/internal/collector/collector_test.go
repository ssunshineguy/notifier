package collector_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/collector"
)

type deleteBatchFunc func(context.Context, time.Time, int) (int64, error)

func (f deleteBatchFunc) DeleteStoppedNotifications(ctx context.Context, before time.Time, limit int) (int64, error) {
	return f(ctx, before, limit)
}

func TestRunKeepsExactCutoffAcrossBatchesAndCanRepeat(t *testing.T) {
	cutoff := time.Date(2025, 11, 15, 10, 20, 30, 123456789, time.FixedZone("UTC+3", 3*60*60))
	batches := []int64{3, 1, 2, 0, 0}
	calls := 0
	store := deleteBatchFunc(func(ctx context.Context, before time.Time, limit int) (int64, error) {
		if ctx != t.Context() || before != cutoff || limit != 3 {
			t.Errorf("batch cutoff = %v, limit = %d; want exact supplied %v and 3 with caller context", before, limit, cutoff)
		}
		if calls >= len(batches) {
			t.Fatal("collector continued after an empty batch")
		}
		deleted := batches[calls]
		calls++
		return deleted, nil
	})
	for run, want := range []int64{6, 0} {
		deleted, err := collector.Run(t.Context(), store, 3, cutoff)
		if err != nil || deleted != want {
			t.Fatalf("run %d = (%d, %v), want (%d, nil)", run, deleted, err, want)
		}
	}
	if calls != len(batches) {
		t.Fatalf("batch calls = %d, want %d; a partial batch must not terminate the run", calls, len(batches))
	}
}

func TestRunStopsBetweenCommittedBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	store := deleteBatchFunc(func(context.Context, time.Time, int) (int64, error) {
		calls++
		cancel()
		return 2, nil
	})
	cutoff := time.Date(2025, 11, 15, 0, 0, 0, 0, time.UTC)
	deleted, err := collector.Run(ctx, store, 2, cutoff)
	if !errors.Is(err, context.Canceled) || deleted != 2 || calls != 1 {
		t.Fatalf("canceled collection = (%d, %v), calls = %d; want 2 committed rows and one call", deleted, err, calls)
	}
	deleted, err = collector.Run(ctx, store, 2, cutoff)
	if !errors.Is(err, context.Canceled) || deleted != 0 || calls != 1 {
		t.Fatalf("already canceled collection = (%d, %v), calls = %d; want no new batch", deleted, err, calls)
	}
}

func TestRunReturnsCommittedCountAndStorageError(t *testing.T) {
	for _, failure := range []error{errors.New("batch failed"), context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			calls := 0
			store := deleteBatchFunc(func(context.Context, time.Time, int) (int64, error) {
				calls++
				if calls == 1 {
					return 2, nil
				}
				return 0, failure
			})
			deleted, err := collector.Run(t.Context(), store, 2, time.Date(2025, 11, 15, 0, 0, 0, 0, time.UTC))
			if !errors.Is(err, failure) || deleted != 2 || calls != 2 {
				t.Fatalf("failed collection = (%d, %v), calls = %d; want 2 committed rows and wrapped storage error", deleted, err, calls)
			}
		})
	}
}

func TestRunDoesNotOverlapBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var calls, active atomic.Int32
		store := deleteBatchFunc(func(context.Context, time.Time, int) (int64, error) {
			if concurrent := active.Add(1); concurrent != 1 {
				t.Errorf("concurrent batch calls = %d, want 1", concurrent)
			}
			defer active.Add(-1)
			if calls.Add(1) == 1 {
				close(started)
				<-release
				return 1, nil
			}
			return 0, nil
		})
		type result struct {
			deleted int64
			err     error
		}
		done := make(chan result, 1)
		go func() {
			deleted, err := collector.Run(t.Context(), store, 1, time.Date(2025, 11, 15, 0, 0, 0, 0, time.UTC))
			done <- result{deleted: deleted, err: err}
		}()
		<-started
		synctest.Wait()
		if calls.Load() != 1 {
			t.Errorf("batch calls while the first is blocked = %d, want 1", calls.Load())
		}
		select {
		case got := <-done:
			t.Fatalf("Run returned before storage finished: %+v", got)
		default:
		}
		close(release)
		got := <-done
		if got.err != nil || got.deleted != 1 || calls.Load() != 2 || active.Load() != 0 {
			t.Fatalf("Run = (%d, %v), calls %d, active %d; want 1 deleted and two sequential completed calls", got.deleted, got.err, calls.Load(), active.Load())
		}
	})
}

func TestRunCancellationReachesCurrentBatchAndWaitsForItsReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan context.Context, 1)
		release := make(chan struct{})
		var calls atomic.Int32
		store := deleteBatchFunc(func(batchCtx context.Context, _ time.Time, _ int) (int64, error) {
			calls.Add(1)
			started <- batchCtx
			<-release
			return 1, nil
		})
		type result struct {
			deleted int64
			err     error
		}
		done := make(chan result, 1)
		go func() {
			deleted, err := collector.Run(ctx, store, 1, time.Date(2025, 11, 15, 0, 0, 0, 0, time.UTC))
			done <- result{deleted: deleted, err: err}
		}()
		batchCtx := <-started
		cancel()
		synctest.Wait()
		if !errors.Is(batchCtx.Err(), context.Canceled) {
			t.Errorf("current batch error = %v, want cancellation", batchCtx.Err())
		}
		select {
		case got := <-done:
			t.Fatalf("Run left a batch running after return: %+v", got)
		default:
		}
		close(release)
		got := <-done
		if !errors.Is(got.err, context.Canceled) || got.deleted != 1 || calls.Load() != 1 {
			t.Fatalf("Run = (%d, %v), calls %d; want committed count and cancellation without another batch", got.deleted, got.err, calls.Load())
		}
	})
}

func TestRunRejectsInvalidBatchBeforeCallingStore(t *testing.T) {
	store := deleteBatchFunc(func(context.Context, time.Time, int) (int64, error) {
		t.Fatal("invalid batch size reached storage")
		return 0, nil
	})
	for _, batchSize := range []int{-1, 0} {
		if deleted, err := collector.Run(t.Context(), store, batchSize, time.Time{}); err == nil || deleted != 0 {
			t.Errorf("batch size %d = (%d, %v), want rejection", batchSize, deleted, err)
		}
	}
}

func TestRunAcceptsPositiveBatchSizes(t *testing.T) {
	for _, batchSize := range []int{1, 1000, 1001} {
		calls := 0
		store := deleteBatchFunc(func(_ context.Context, _ time.Time, limit int) (int64, error) {
			calls++
			if limit != batchSize {
				t.Errorf("batch limit = %d, want %d", limit, batchSize)
			}
			return 0, nil
		})
		if deleted, err := collector.Run(t.Context(), store, batchSize, time.Time{}); err != nil || deleted != 0 || calls != 1 {
			t.Errorf("batch size %d = (%d, %v), calls %d; want empty successful collection", batchSize, deleted, err, calls)
		}
	}
}
