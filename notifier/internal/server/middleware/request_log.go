package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
)

const responseLogLimit = 8 * 1024

type requestStartedKey struct{}

// LogAPIRequests records the HTTP boundary, including responses from admission and timeout middleware.
func LogAPIRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := context.WithValue(r.Context(), requestStartedKey{}, start)
		route, notificationID := logRoute(r.URL.Path)
		healthProbe := r.Method == http.MethodGet && (route == "/health/live" || route == "/health/ready")
		requestLogger := logger.With("request_id", logRequestID(r.Header.Get(requestIDHeader)),
			"method", logMethod(r.Method), "route", route)
		if notificationID != "" {
			requestLogger = requestLogger.With("notification_id", notificationID)
		}
		ctx = logging.WithLogger(ctx, requestLogger)
		if !requestLogger.Enabled(ctx, slog.LevelInfo) {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		startLevel := slog.LevelInfo
		if healthProbe {
			startLevel = slog.LevelDebug
		}
		if requestLogger.Enabled(ctx, startLevel) {
			requestLogger.Log(ctx, startLevel, "Start API Request",
				"headers", logHeaders(r.Header), "has_query", r.URL.RawQuery != "", "content_length", r.ContentLength)
		}

		response := &loggedResponse{ResponseWriter: w, ctx: ctx, logger: requestLogger, method: r.Method}
		defer func() {
			durationMS := time.Since(start).Milliseconds()
			response.logBody()
			outcome := "sent"
			if response.status == 0 {
				outcome = "aborted"
			}
			if response.writeFailed {
				outcome = "write_error"
			}
			endLevel := slog.LevelInfo
			if healthProbe && outcome == "sent" && response.status >= 200 && response.status < 300 {
				endLevel = slog.LevelDebug
			}
			requestLogger.Log(ctx, endLevel, fmt.Sprintf("End API Request duration %d ms", durationMS),
				"status", response.status, "outcome", outcome, "headers", response.headers,
				"duration_ms", durationMS, "bytes_written", response.written)
		}()
		next.ServeHTTP(response, r.WithContext(ctx))
		if response.status == 0 && ctx.Err() == nil {
			response.recordStatus(http.StatusOK)
		}
	})
}

func logRoute(path string) (route, notificationID string) {
	switch path {
	case "/health/live", "/health/ready", "/v1/notifications":
		return path, ""
	}
	if value, ok := strings.CutPrefix(path, "/v1/notifications/"); ok && value != "" && !strings.Contains(value, "/") {
		id, err := uuid.Parse(value)
		if err == nil {
			return "/v1/notifications/{notification_id}", id.String()
		}
		return "/v1/notifications/{notification_id}", "[invalid]"
	}
	return "[unmatched]", ""
}

func logMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch,
		http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "[other]"
	}
}

func logRequestID(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 64 && strings.IndexFunc(value, func(r rune) bool {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '-', r == '_', r == '.':
			return false
		default:
			return true
		}
	}) == -1 {
		return value
	}
	return logging.Redacted("request_id").Value.String()
}

func logHeaders(headers http.Header) map[string]any {
	result := make(map[string]any)
	other := 0
	for name, values := range headers {
		key := http.CanonicalHeaderKey(name)
		if len(values) == 0 {
			continue
		}
		value := values[0]
		redacted := logging.Redacted(key).Value.String()
		switch key {
		case "Content-Type", "Accept":
			mediaType, _, err := mime.ParseMediaType(value)
			switch mediaType {
			case "application/json", "application/problem+json", "*/*", "application/*":
				if err == nil {
					result[key] = mediaType
					continue
				}
			}
			result[key] = redacted
		case "X-Request-Id":
			result[key] = logRequestID(value)
		case "Content-Length":
			if size, err := strconv.ParseUint(value, 10, 63); err == nil {
				result[key] = size
			} else {
				result[key] = redacted
			}
		case "Connection":
			switch strings.ToLower(value) {
			case "close", "keep-alive":
				result[key] = strings.ToLower(value)
			default:
				result[key] = redacted
			}
		case "Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Client-Id":
			result[key] = redacted
		default:
			other++
		}
	}
	if other > 0 {
		result["other_header_count"] = other
	}
	return result
}

type loggedResponse struct {
	http.ResponseWriter
	ctx         context.Context
	logger      *slog.Logger
	method      string
	status      int
	headers     map[string]any
	written     int
	writeFailed bool
	body        bytes.Buffer
}

// Unwrap lets http.ResponseController reach optional operations on the original writer.
func (w *loggedResponse) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// WriteHeader forwards interim responses and records the first final status.
func (w *loggedResponse) WriteHeader(status int) {
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.recordStatus(status)
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write counts only the bytes accepted by the underlying writer.
func (w *loggedResponse) Write(p []byte) (int, error) {
	w.recordStatus(http.StatusOK)
	n, err := w.ResponseWriter.Write(p)
	w.written += n
	w.writeFailed = w.writeFailed || err != nil
	if w.logger.Enabled(w.ctx, w.bodyLevel()) && w.written <= responseLogLimit {
		w.body.Write(p[:n])
	}
	return n, err
}

func (w *loggedResponse) recordStatus(status int) {
	if w.status == 0 {
		w.status = status
		w.headers = logHeaders(w.Header())
	}
}

func (w *loggedResponse) bodyLevel() slog.Level {
	if w.method == http.MethodGet && w.status < http.StatusBadRequest {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func (w *loggedResponse) logBody() {
	level := w.bodyLevel()
	if w.written == 0 || !w.logger.Enabled(w.ctx, level) {
		return
	}
	if w.written > responseLogLimit {
		w.logger.Log(w.ctx, level, "API Response Parameters", "body_omitted", "size_limit")
		return
	}

	var body struct {
		NotificationID     *uuid.UUID                 `json:"notification_id"`
		ExternalID         json.RawMessage            `json:"external_id"`
		NotificationStatus codegen.NotificationStatus `json:"notification_status"`
		NotificationParams codegen.NotificationParams `json:"notification_params"`
		Deadline           *time.Time                 `json:"deadline_dt"`
		Start              *time.Time                 `json:"start_dt"`
		ProblemType        string                     `json:"type"`
		Status             json.RawMessage            `json:"status"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &body); err != nil {
		return
	}
	fields := make(map[string]any)
	if body.NotificationID != nil {
		fields["notification_id"] = body.NotificationID
	}
	if body.ExternalID != nil {
		redacted := logging.Redacted("external_id")
		fields[redacted.Key] = redacted.Value.String()
	}
	if body.NotificationStatus != "" {
		fields["notification_status"] = body.NotificationStatus
	}
	if len(body.NotificationParams) != 0 {
		fields["notification_params"] = body.NotificationParams
	}
	if body.Deadline != nil {
		fields["deadline_dt"] = body.Deadline
	}
	if body.Start != nil {
		fields["start_dt"] = body.Start
	}
	if w.status >= http.StatusBadRequest && strings.HasPrefix(body.ProblemType, "/problems/") {
		fields["type"] = body.ProblemType
		fields["status"] = w.status
	}
	if w.status == http.StatusOK && string(body.Status) == `"ok"` {
		fields["status"] = "ok"
	}
	if len(fields) != 0 {
		w.logger.Log(w.ctx, level, "API Response Parameters", "parameters", fields)
	}
}
