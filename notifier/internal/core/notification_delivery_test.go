package core_test

import (
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

func TestAfterDeliveryStopsAcceptedNotifications(t *testing.T) {
	for _, status := range []string{core.EventSuccess, core.EventDeadlineExceeded} {
		t.Run(status, func(t *testing.T) {
			result := core.AfterDelivery(status, 3, true, time.Time{})
			if result.FailedAttemptCount != 3 || result.NotificationStatus != status ||
				result.DeliveryStatus != core.DeliveryDelivered || result.NextScheduled != nil {
				t.Errorf("result = %+v, want %q delivered with three failures", result, status)
			}
		})
	}
}

func TestAfterDeliverySchedulesUpTo42Attempts(t *testing.T) {
	finishedAt := time.Date(2050, time.January, 1, 0, 0, 0, 0, time.UTC)
	delaySeconds := [...]int{4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}
	for attempt := 1; attempt < 42; attempt++ {
		result := core.AfterDelivery(core.EventSuccess, attempt-1, false, finishedAt)
		wantNext := finishedAt
		if attempt%3 == 0 {
			wantNext = wantNext.Add(time.Duration(delaySeconds[attempt/3-1]) * time.Second)
		}
		if result.FailedAttemptCount != attempt || result.NotificationStatus != core.EventSuccess || result.DeliveryStatus != core.DeliveryTryingToDeliver ||
			result.NextScheduled == nil || !result.NextScheduled.Equal(wantNext) {
			t.Errorf("failure %d yields %+v, want active with next attempt at %v", attempt, result, wantNext)
		}
	}
	last := core.AfterDelivery(core.EventDeadlineExceeded, 41, false, finishedAt)
	if last.FailedAttemptCount != 42 || last.NotificationStatus != core.EventDeadlineExceeded ||
		last.DeliveryStatus != core.DeliveryFailed || last.NextScheduled != nil {
		t.Errorf("last result = %+v, want delivery_failed and no next attempt", last)
	}
}
