package core_test

import (
	"math"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

func TestNewNotificationInitialCheckDelay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		delay    time.Duration
		deadline *time.Time
	}{
		{name: "zero delay"},
		{name: "hours", delay: 6 * time.Hour},
		{name: "explicit deadline before first check", delay: 6 * time.Hour, deadline: at(time.Hour)},
		{name: "default deadline before first check", delay: client.MaxLifetime + time.Hour},
		{name: "largest whole-second duration", delay: time.Duration(math.MaxInt64/int64(time.Second)) * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := core.CreateRequest{ExternalID: "initial-delay", Params: []core.Condition{leaf(now)}, Deadline: tc.deadline}
			notification, err := core.NewNotification(client, req, now, tc.delay)
			if err != nil {
				t.Fatal(err)
			}
			if !notification.Start.Equal(now) || !notification.NextScheduled.Equal(now.Add(tc.delay)) {
				t.Errorf("start = %v, next check = %v, want %v and %v", notification.Start, notification.NextScheduled, now, now.Add(tc.delay))
			}
			wantDeadline := now.Add(client.MaxLifetime)
			if tc.deadline != nil {
				wantDeadline = *tc.deadline
			}
			if !notification.Deadline.Equal(wantDeadline) {
				t.Errorf("deadline = %v, want %v", notification.Deadline, wantDeadline)
			}
		})
	}
}
