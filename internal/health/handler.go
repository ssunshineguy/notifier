// Package health serves process probes on a dedicated loopback listener.
package health

import (
	"context"
	"io"
	"net/http"

	"github.com/ssunshineguy/notifier/internal/logging"
)

// NewHandler builds the probe routes with an optional readiness check.
func NewHandler(checkReady func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		writeResponse(w, r, nil)
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		writeResponse(w, r, checkReady)
	})
	return mux
}

func writeResponse(w http.ResponseWriter, r *http.Request, checkReady func(context.Context) error) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status := http.StatusOK
	contentType := "application/json; charset=utf-8"
	body := `{"status":"ok"}`
	if checkReady != nil && checkReady(r.Context()) != nil {
		status = http.StatusServiceUnavailable
		contentType = "application/problem+json"
		body = `{"type":"/problems/not-ready","title":"Service is not ready","status":503,"detail":"database is unreachable"}`
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		logging.FromContext(r.Context()).DebugContext(r.Context(), "write health response")
	}
}
