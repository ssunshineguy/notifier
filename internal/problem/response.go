// Package problem writes HTTP Problem Details shared by the two services.
package problem

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ssunshineguy/notifier/internal/logging"
)

// Response carries the fields used by HTTP middleware errors.
type Response struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError emits a generic response for a router error.
func WriteError(ctx context.Context, w http.ResponseWriter, r *http.Request, status int) {
	response := Response{Type: "about:blank", Title: http.StatusText(status), Status: status}
	switch status {
	case http.StatusBadRequest:
		response.Type = "/problems/validation-error"
		response.Title = "Request validation failed"
	case http.StatusUnauthorized:
		response.Type = "/problems/unauthorized"
	case http.StatusInternalServerError:
		response.Type = "/problems/internal-error"
		response.Title = "Internal server error"
	}
	Write(ctx, w, r, response)
}

// Write emits a problem response and echoes the request ID when present.
func Write(ctx context.Context, w http.ResponseWriter, r *http.Request, response Response) {
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
		response.RequestID = requestID
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(response.Status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logging.FromContext(ctx).WarnContext(ctx, "write problem response", "error_type", fmt.Sprintf("%T", err))
	}
}
