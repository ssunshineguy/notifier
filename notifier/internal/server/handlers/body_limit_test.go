package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/bodylimit"
	"github.com/ssunshineguy/notifier/internal/problem"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestNotificationBodyLimit(t *testing.T) {
	const valid = `{"external_id":"size-check","notification_params":[{"type":"price","operation":"greater_than","value":9223372036854775807}]}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, unknownLength := range []bool{false, true} {
			for _, extra := range []int{0, 1} {
				t.Run(method+"/"+map[bool]string{false: "content-length", true: "chunked"}[unknownLength]+"/"+map[int]string{0: "boundary", 1: "oversized"}[extra], func(t *testing.T) {
					id := uuid.New()
					store := &notificationStoreStub{id: id}
					updates := 0
					store.updateNotification = func(_ context.Context, _ uuid.UUID, _ string, conditions []core.Condition) (core.NotificationDetails, error) {
						updates++
						return core.NotificationDetails{ID: id, ExternalID: "size-check", Params: conditions, Start: time.Now(), Deadline: time.Now().Add(time.Hour), NotificationStatus: core.NotificationActive}, nil
					}
					router, err := codegen.NewServer(NewHandler(store, time.Second), middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: "size-client", MaxLifetime: time.Hour}}), codegen.WithErrorHandler(middleware.HandleError))
					if err != nil {
						t.Fatal(err)
					}
					payload := valid
					path := "/v1/notifications"
					if method == http.MethodPut {
						payload = `{"notification_params":[{"type":"price","operation":"greater_than","value":9223372036854775807}]}`
						path += "/" + id.String()
					}
					payload += strings.Repeat(" ", int(bodylimit.Bytes)-len(payload)+extra)
					req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(payload))
					if unknownLength {
						req.ContentLength = -1
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Client-ID", "size-client")
					req.Header.Set("X-Request-ID", "body-limit")
					res := httptest.NewRecorder()
					bodylimit.Requests(middleware.RequestTimeout(time.Second, router)).ServeHTTP(res, req)
					if extra == 0 {
						if res.Code != http.StatusOK {
							t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
						}
						if len(store.saved)+updates != 1 {
							t.Fatal("boundary body did not reach storage")
						}
						return
					}
					if res.Code != http.StatusRequestEntityTooLarge {
						t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
					}
					if len(store.saved)+updates != 0 {
						t.Fatal("oversized body reached storage")
					}
					var response problem.Response
					if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.Type != "/problems/content-too-large" || response.Status != 413 || response.RequestID != "body-limit" || res.Header().Get("Content-Type") != "application/problem+json" || res.Header().Get("X-Request-ID") != "body-limit" || res.Header().Get("Connection") != "close" {
						t.Fatalf("unexpected response: %+v, headers: %v", response, res.Header())
					}
				})
			}
		}
	}
}

func TestIncompleteOversizedBodyReturns413(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(map[bool]string{false: "declared-length", true: "unfinished-chunk"}[chunked], func(t *testing.T) {
			store := &notificationStoreStub{id: uuid.New()}
			router, err := codegen.NewServer(NewHandler(store, time.Second), middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: "size-client", MaxLifetime: time.Hour}}), codegen.WithErrorHandler(middleware.HandleError))
			if err != nil {
				t.Fatal(err)
			}
			handler := bodylimit.Requests(middleware.RequestTimeout(5*time.Second, router))
			server := httptest.NewServer(handler)
			defer server.Close()
			dialer := net.Dialer{Timeout: time.Second}
			conn, err := dialer.DialContext(t.Context(), "tcp", strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			header := fmt.Sprintf("Content-Length: %d\r\n", bodylimit.Bytes+4096)
			body := ""
			if chunked {
				header = "Transfer-Encoding: chunked\r\n"
				body = fmt.Sprintf("%x\r\n", bodylimit.Bytes+4096) + strings.Repeat(" ", int(bodylimit.Bytes)+1)
			}
			request := "POST /v1/notifications HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-Client-ID: size-client\r\nX-Request-ID: partial-body\r\n" + header + "\r\n" + body
			if _, err := io.WriteString(conn, request); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("read response before completing the body: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			var result problem.Response
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 413 || result.Type != "/problems/content-too-large" || result.RequestID != "partial-body" || !response.Close {
				t.Fatalf("response = %d %+v, close = %v", response.StatusCode, result, response.Close)
			}
		})
	}
}
