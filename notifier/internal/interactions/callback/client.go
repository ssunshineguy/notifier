// Package callback sends notification events over HTTP.
package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// Client delivers notifications to configured client endpoints.
type Client struct {
	http *http.Client
}

// NewClient creates a callback sender that treats redirects as failed attempts.
func NewClient() *Client {
	return &Client{http: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Send posts a notification event and accepts any HTTP status in the 2xx range.
func (c *Client) Send(ctx context.Context, endpoint, requestID string, event core.NotificationEvent) (int, error) {
	payload := struct {
		NotificationID uuid.UUID `json:"notification_id"`
		ExternalID     string    `json:"external_id"`
		Status         string    `json:"notification_status"`
		SentAt         time.Time `json:"sent_dt"`
	}{
		NotificationID: event.NotificationID,
		ExternalID:     event.ExternalID,
		Status:         event.Status,
		SentAt:         event.SentAt.UTC(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode callback event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	response, err := c.http.Do(req) //nolint:gosec // The destination comes from trusted client settings.
	if err != nil {
		return 0, fmt.Errorf("send callback: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response.StatusCode, fmt.Errorf("callback returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}
