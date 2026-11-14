package core_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

func at(d time.Duration) *time.Time {
	t := now.Add(d)
	return &t
}

func TestNewNotificationDeadline(t *testing.T) {
	inHour := leaf(now.Add(time.Hour))
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
			name:         "default deadline before the time threshold",
			params:       []core.Condition{leaf(now.Add(maxLifetime + time.Hour))},
			wantDeadline: now.Add(maxLifetime),
		},
		{
			name:     "now is not in the future",
			params:   []core.Condition{inHour},
			deadline: at(0),
			want:     []core.FieldError{{Field: "/deadline_dt", Message: "must be in the future"}},
		},
		{
			name:         "explicit deadline before the time threshold",
			params:       []core.Condition{leaf(now.Add(maxLifetime + time.Hour))},
			deadline:     at(time.Hour),
			wantDeadline: now.Add(time.Hour),
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
