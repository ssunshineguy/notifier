package core_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/internal/core"
)

func at(d time.Duration) *time.Time {
	t := now.Add(d)
	return &t
}

func TestNewNotificationDeadline(t *testing.T) {
	inHour := leaf(now.Add(time.Hour))
	inDay := leaf(now.Add(24 * time.Hour))
	hourAgo := leaf(now.Add(-time.Hour))
	counter := core.Condition{Type: "counter"}
	maxLifetime := client.MaxLifetime

	tests := []struct {
		name         string
		params       []core.Condition
		deadline     *time.Time
		want         []core.FieldError
		wantDeadline time.Time
	}{
		{
			name:         "omitted: start plus max lifetime",
			params:       []core.Condition{inHour},
			wantDeadline: now.Add(maxLifetime),
		},
		{
			name:         "omitted: not checked against conditions",
			params:       []core.Condition{leaf(now.Add(maxLifetime + time.Hour))},
			wantDeadline: now.Add(maxLifetime),
		},
		{
			name:     "now is not in the future",
			params:   []core.Condition{hourAgo},
			deadline: at(0),
			want:     []core.FieldError{{Field: "/deadline_dt", Message: "must be in the future"}},
		},
		{
			name:     "equal to the time condition",
			params:   []core.Condition{inHour},
			deadline: at(time.Hour),
			want: []core.FieldError{{
				Field:   "/deadline_dt",
				Message: "must be later than the moment the time conditions can be met, 2026-11-13T11:00:00Z",
			}},
		},
		{
			name:         "later than the time condition",
			params:       []core.Condition{inHour},
			deadline:     at(time.Hour + time.Second),
			wantDeadline: now.Add(time.Hour + time.Second),
		},
		{
			name:         "or: the earliest branch counts",
			params:       []core.Condition{or(inDay, inHour)},
			deadline:     at(2 * time.Hour),
			wantDeadline: now.Add(2 * time.Hour),
		},
		{
			name:     "and: the latest branch counts",
			params:   []core.Condition{and(inDay, inHour)},
			deadline: at(2 * time.Hour),
			want: []core.FieldError{{
				Field:   "/deadline_dt",
				Message: "must be later than the moment the time conditions can be met, 2026-11-14T10:00:00Z",
			}},
		},
		{
			name:         "or with a non-time leaf: no bound",
			params:       []core.Condition{or(inDay, counter)},
			deadline:     at(2 * time.Hour),
			wantDeadline: now.Add(2 * time.Hour),
		},
		{
			name:     "and with a non-time leaf: time leaf still bounds",
			params:   []core.Condition{and(inDay, counter)},
			deadline: at(2 * time.Hour),
			want: []core.FieldError{{
				Field:   "/deadline_dt",
				Message: "must be later than the moment the time conditions can be met, 2026-11-14T10:00:00Z",
			}},
		},
		{
			name:         "exactly max lifetime",
			params:       []core.Condition{inHour},
			deadline:     at(maxLifetime),
			wantDeadline: now.Add(maxLifetime),
		},
		{
			name:     "beyond max lifetime",
			params:   []core.Condition{inHour},
			deadline: at(maxLifetime + time.Second),
			want:     []core.FieldError{{Field: "/deadline_dt", Message: "exceeds the maximum notification lifetime for the client"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := core.CreateRequest{ExternalID: "order-1", Params: tt.params, Deadline: tt.deadline}
			n, err := core.NewNotification(client, req, now, time.Minute)
			if got := fieldErrors(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("field errors:\n got %v\nwant %v", got, tt.want)
			}
			if err != nil {
				return
			}
			if !n.Deadline.Equal(tt.wantDeadline) {
				t.Errorf("deadline: got %v, want %v", n.Deadline, tt.wantDeadline)
			}
			if !n.Start.Equal(now) || !n.NextScheduled.Equal(now.Add(time.Minute)) {
				t.Errorf("start %v, next scheduled %v: want %v and a minute later", n.Start, n.NextScheduled, now)
			}
		})
	}
}
