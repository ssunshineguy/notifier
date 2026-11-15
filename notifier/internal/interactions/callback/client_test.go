package callback_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/callback"
)

func TestSendCallbackPayload(t *testing.T) {
	event := core.NotificationEvent{
		NotificationID: uuid.New(),
		ExternalID:     "test-event",
		Status:         core.EventDeadlineExceeded,
		SentAt:         time.Date(2050, time.January, 1, 3, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60)),
	}
	const requestID = "fixture-id"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/client/callback?fixture=true" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Request-ID") != requestID {
			t.Errorf("callback request = %s %s with headers %v", r.Method, r.URL, r.Header)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload) != 4 || payload["notification_id"] != event.NotificationID.String() || payload["external_id"] != event.ExternalID ||
			payload["notification_status"] != event.Status || payload["sent_dt"] != "2050-01-01T00:00:00Z" {
			t.Errorf("callback payload = %v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	status, err := callback.NewClient().Send(t.Context(), server.URL+"/client/callback?fixture=true", requestID, event)
	if err != nil || status != http.StatusNoContent {
		t.Errorf("Send = (%d, %v), want (204, nil)", status, err)
	}
}

func TestSendRejectsNon2xxAndDoesNotFollowRedirect(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					t.Error("callback followed a redirect")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			gotStatus, err := callback.NewClient().Send(t.Context(), server.URL, "test-id", core.NotificationEvent{})
			if err == nil || gotStatus != status {
				t.Errorf("Send = (%d, %v), want (%d, non-nil error)", gotStatus, err, status)
			}
		})
	}
}

func TestSendRespectsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("callback reached the server despite cancellation")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	status, err := callback.NewClient().Send(ctx, server.URL, "test-id", core.NotificationEvent{})
	if !errors.Is(err, context.Canceled) || status != 0 {
		t.Errorf("Send = (%d, %v), want (0, canceled)", status, err)
	}
}
