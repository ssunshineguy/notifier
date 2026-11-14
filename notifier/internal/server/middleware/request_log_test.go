package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestLogAPIRequests(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		level    slog.Level
		status   int
		body     string
		wantBody bool
	}{
		{"GET at INFO", http.MethodGet, slog.LevelInfo, http.StatusOK, `{"notification_status":"active","external_id":"private-id"}`, false},
		{"GET at DEBUG", http.MethodGet, slog.LevelDebug, http.StatusOK, `{"notification_status":"active","external_id":"private-id"}`, true},
		{"POST at INFO", http.MethodPost, slog.LevelInfo, http.StatusOK, `{"notification_id":"0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71"}`, true},
		{"GET failure at INFO", http.MethodGet, slog.LevelInfo, http.StatusNotFound, `{"type":"/problems/not-found","status":404,"detail":"private-detail"}`, true},
		{"large body", http.MethodPost, slog.LevelInfo, http.StatusOK, strings.Repeat("x", 8193), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: tt.level}))
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(logs.String(), "Start API Request") {
					t.Error("missing start event before the handler")
				}
				logging.FromContext(r.Context()).InfoContext(r.Context(), "Inside Request")
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "trace-42")
				w.Header().Set("Set-Cookie", "private-cookie")
				w.WriteHeader(tt.status)
				if _, err := io.WriteString(w, tt.body); err != nil {
					t.Error(err)
				}
			})
			req := httptest.NewRequestWithContext(t.Context(), tt.method, "/v1/notifications/0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71?token=private-query", nil)
			req.Header.Set("X-Request-ID", "trace-42")
			req.Header.Set("Authorization", "private-auth")
			req.Header.Set("X-Client-ID", "private-client")
			req.Header.Set("Content-Type", "application/json; token=private-content-type")
			req.Header.Set("Accept", "application/json; token=private-accept")
			req.Header.Set("X-Private-Name", "private-custom-value")
			resp := httptest.NewRecorder()
			middleware.LogAPIRequests(logger, next).ServeHTTP(resp, req)

			if resp.Code != tt.status || resp.Body.String() != tt.body || resp.Header().Get("Set-Cookie") != "private-cookie" {
				t.Fatalf("unexpected HTTP response: %d %s %s", resp.Code, resp.Header(), resp.Body)
			}
			events := requestLogEvents(t, &logs)
			if len(events) != 3 && len(events) != 4 {
				t.Fatalf("got %d events, want 3 or 4", len(events))
			}
			end := events[len(events)-1]
			if events[0]["msg"] != "Start API Request" || events[1]["msg"] != "Inside Request" ||
				!isEndRequestLog(end) || end["status"] != float64(tt.status) {
				t.Fatalf("unexpected event sequence: %+v", events)
			}
			for _, event := range events {
				if event["request_id"] != "trace-42" {
					t.Errorf("request_id missing from %+v", event)
				}
			}
			if found := len(events) == 4 && events[2]["msg"] == "API Response Parameters"; found != tt.wantBody {
				t.Errorf("response parameters logged = %t, want %t", found, tt.wantBody)
			}
			if tt.wantBody && len(events) == 4 {
				body := events[2]
				level := "INFO"
				if tt.method == http.MethodGet && tt.status == http.StatusOK {
					level = "DEBUG"
				}
				if body["level"] != level {
					t.Errorf("body log level = %v, want %s", body["level"], level)
				}
				parameters, _ := body["parameters"].(map[string]any)
				switch {
				case len(tt.body) > 8192:
					if body["body_omitted"] != "size_limit" {
						t.Errorf("unexpected truncated response log: %v", body)
					}
				case tt.status == http.StatusNotFound:
					if parameters["type"] != "/problems/not-found" || parameters["status"] != float64(tt.status) {
						t.Errorf("unexpected problem parameters: %v", body)
					}
				case tt.method == http.MethodGet:
					if parameters["notification_status"] != "active" || parameters["external_id"] != "[redacted]" {
						t.Errorf("unexpected GET parameters: %v", body)
					}
				default:
					if parameters["notification_id"] != "0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71" {
						t.Errorf("unexpected POST parameters: %v", body)
					}
				}
			}
			inHeaders, _ := events[0]["headers"].(map[string]any)
			outHeaders, _ := end["headers"].(map[string]any)
			if inHeaders["Content-Type"] != "application/json" || inHeaders["Accept"] != "application/json" ||
				inHeaders["Authorization"] != "[redacted]" || inHeaders["X-Client-Id"] != "[redacted]" ||
				inHeaders["other_header_count"] != float64(1) || outHeaders["Set-Cookie"] != "[redacted]" ||
				strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "X-Private-Name") {
				t.Errorf("unexpected log redaction: %s", logs.String())
			}
		})
	}
}

func TestLogAPIRequestsHealthLevels(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		level  slog.Level
		status int
	}{
		{"liveness at INFO", "/health/live", slog.LevelInfo, http.StatusOK},
		{"readiness at INFO", "/health/ready", slog.LevelInfo, http.StatusOK},
		{"liveness at DEBUG", "/health/live", slog.LevelDebug, http.StatusOK},
		{"readiness at DEBUG", "/health/ready", slog.LevelDebug, http.StatusOK},
		{"readiness failure at INFO", "/health/ready", slog.LevelInfo, http.StatusServiceUnavailable},
		{"readiness failure at DEBUG", "/health/ready", slog.LevelDebug, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: tt.level}))
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				body := `{"status":"ok"}`
				if tt.status != http.StatusOK {
					body = `{"type":"/problems/not-ready","status":503}`
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Fatal(err)
				}
			})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			req.Header.Set("X-Request-ID", "health-42")
			resp := httptest.NewRecorder()
			middleware.LogAPIRequests(logger, next).ServeHTTP(resp, req)
			if resp.Code != tt.status {
				t.Fatalf("HTTP status = %d, want %d", resp.Code, tt.status)
			}

			if tt.status == http.StatusOK && tt.level == slog.LevelInfo {
				if logs.Len() != 0 {
					t.Fatalf("success at INFO emitted logs: %s", &logs)
				}
				return
			}

			want := []string{"API Response Parameters"}
			if tt.level == slog.LevelDebug {
				want = append([]string{"Start API Request"}, want...)
			}
			events := requestLogEvents(t, &logs)
			if len(events) != len(want)+1 {
				t.Fatalf("got %v, want %v followed by a completion log", events, want)
			}
			for i, event := range events {
				level := "DEBUG"
				if tt.status != http.StatusOK && event["msg"] != "Start API Request" {
					level = "INFO"
				}
				if event["level"] != level || event["request_id"] != "health-42" || event["route"] != tt.path {
					t.Errorf("event %d = %v, want level %s for %s", i, event, level, tt.path)
				}
				if i < len(want) && event["msg"] != want[i] {
					t.Errorf("event %d = %v, want %s", i, event, want[i])
				}
			}
			if end := events[len(events)-1]; !isEndRequestLog(end) || end["status"] != float64(tt.status) {
				t.Errorf("unexpected end event: %v", end)
			}
		})
	}
}

func TestLogAPIRequestsHealthPathWithWrongMethod(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/health/ready", nil)
	resp := httptest.NewRecorder()
	middleware.LogAPIRequests(logger, next).ServeHTTP(resp, req)

	events := requestLogEvents(t, &logs)
	if len(events) != 2 || events[0]["msg"] != "Start API Request" || events[0]["level"] != "INFO" ||
		!isEndRequestLog(events[1]) || events[1]["level"] != "INFO" ||
		events[1]["status"] != float64(http.StatusMethodNotAllowed) {
		t.Fatalf("unexpected log events: %v", events)
	}
}

func TestLogAPIRequestsRecordsTimeoutAndOverload(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			assertLoggedStatus(t, middleware.RequestTimeout(time.Second, next), http.StatusServiceUnavailable)
		})
	})
	t.Run("overload", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		assertLoggedStatus(t, middleware.LimitInFlight(0, 0, 0, next), http.StatusTooManyRequests)
	})
}

func assertLoggedStatus(t *testing.T, next http.Handler, status int) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/notifications", nil)
	resp := httptest.NewRecorder()
	middleware.LogAPIRequests(logger, next).ServeHTTP(resp, req)
	if resp.Code != status {
		t.Fatalf("HTTP status = %d, want %d", resp.Code, status)
	}
	events := requestLogEvents(t, &logs)
	if len(events) < 2 || !isEndRequestLog(events[len(events)-1]) ||
		events[len(events)-1]["status"] != float64(status) {
		t.Errorf("missing end event for %d: %v", status, events)
	}
}

func isEndRequestLog(event map[string]any) bool {
	durationMS, ok := event["duration_ms"].(float64)
	return ok && durationMS >= 0 && event["msg"] == fmt.Sprintf("End API Request duration %d ms", int64(durationMS))
}

func requestLogEvents(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

type slowStartLog struct {
	bytes.Buffer
}

func (sink *slowStartLog) Write(data []byte) (int, error) {
	if sink.Len() == 0 {
		time.Sleep(750 * time.Millisecond)
	}
	return sink.Buffer.Write(data)
}

func TestLogAPIRequestsCountsStartLogInTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sink slowStartLog
		logger := slog.New(slog.NewJSONHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
		handler := middleware.LogAPIRequests(logger, middleware.RequestTimeout(time.Second,
			http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) != 250*time.Millisecond {
					t.Errorf("remaining request budget = %v, want 250ms", time.Until(deadline))
				}
				<-r.Context().Done()
			})))
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/live", nil)
		resp := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusServiceUnavailable || time.Since(start) != time.Second {
			t.Fatalf("HTTP %d after %v, want 503 after 1s", resp.Code, time.Since(start))
		}
		events := requestLogEvents(t, &sink.Buffer)
		end := events[len(events)-1]
		if end["msg"] != "End API Request duration 1000 ms" || end["duration_ms"] != float64(1000) {
			t.Errorf("unexpected duration in completion log: %v", end)
		}
	})
}

func TestRequestDiagnosticsKeepRequestID(t *testing.T) {
	tests := []struct {
		name  string
		level slog.Level
		next  http.Handler
		want  []string
	}{
		{
			name:  "deadline and response write",
			level: slog.LevelDebug,
			next: middleware.RequestTimeout(time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "response")
			})),
			want: []string{"set request body read deadline", "write HTTP response"},
		},
		{
			name:  "problem write with INFO disabled",
			level: slog.LevelWarn,
			next:  middleware.LimitInFlight(0, 0, 0, http.NotFoundHandler()),
			want:  []string{"write problem response"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: tt.level}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", nil)
			req.Header.Set("X-Request-ID", "diagnostic-42")
			middleware.LogAPIRequests(logger, tt.next).ServeHTTP(failedResponseWriter{httptest.NewRecorder()}, req)

			events := requestLogEvents(t, &logs)
			for _, message := range tt.want {
				var found bool
				for _, event := range events {
					if event["msg"] == message {
						found = event["request_id"] == "diagnostic-42" && event["level"] == tt.level.String() &&
							event["error_type"] == "*errors.errorString"
						break
					}
				}
				if !found {
					t.Errorf("missing correlated diagnostic %q: %v", message, events)
				}
			}
			if strings.Contains(logs.String(), "connection lost") {
				t.Errorf("raw transport error in logs: %s", &logs)
			}
		})
	}
}

type failedResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w failedResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("connection lost")
}

func (w failedResponseWriter) SetReadDeadline(time.Time) error {
	return errors.New("connection lost")
}

func TestLogAPIRequestsRecordsWriteFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "private-connection")
		if _, err := w.Write([]byte("body")); err == nil {
			t.Error("expected write error")
		}
	})
	req := httptest.NewRequestWithContext(t.Context(), "PRIVATE-METHOD", "/private-path", nil)
	req.Header.Set("X-Request-ID", "private/request-id")
	middleware.LogAPIRequests(logger, next).ServeHTTP(failedResponseWriter{httptest.NewRecorder()}, req)
	events := requestLogEvents(t, &logs)
	if len(events) != 2 || events[1]["outcome"] != "write_error" || events[1]["status"] != float64(http.StatusOK) {
		t.Fatalf("unexpected log events: %+v", events)
	}
	if events[0]["request_id"] != "[redacted]" || strings.Contains(strings.ToLower(logs.String()), "private") {
		t.Errorf("unexpected redaction: %s", logs.String())
	}
}
