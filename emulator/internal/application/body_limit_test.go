package application

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/internal/bodylimit"
	"github.com/ssunshineguy/notifier/internal/problem"
)

func TestEmulatorBodyLimit(t *testing.T) {
	for _, endpoint := range []struct{ name, path, body string }{
		{"client-price", clientOverrides + "size-client", `{"parameter":"price","value":9223372036854775807}`},
		{"client-time", clientOverrides + "size-client", `{"parameter":"time","value":"9999-12-31T23:59:59.999999999+00:00"}`},
		{"callback-override", callbackOverrides + firstNotification, `{"status_code":503,"response_timeout":2147483647}`},
		{"callback", "/callback", `{"notification_id":"` + firstNotification + `","external_id":"` + strings.Repeat("x", 128) + `","notification_status":"deadline_exceeded","sent_dt":"9999-12-31T23:59:59.999999999+00:00"}`},
	} {
		for _, unknownLength := range []bool{false, true} {
			for _, extra := range []int{0, 1} {
				t.Run(endpoint.name+"/"+map[bool]string{false: "content-length", true: "chunked"}[unknownLength]+"/"+map[int]string{0: "boundary", 1: "oversized"}[extra], func(t *testing.T) {
					router, err := NewRouter()
					if err != nil {
						t.Fatal(err)
					}
					body := endpoint.body + strings.Repeat(" ", int(bodylimit.Bytes)-len(endpoint.body)+extra)
					req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.path, strings.NewReader(body))
					if unknownLength {
						req.ContentLength = -1
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Request-ID", "emulator-body-limit")
					res := httptest.NewRecorder()
					router.ServeHTTP(res, req)
					if extra == 0 {
						if res.Code != http.StatusOK {
							t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
						}
						return
					}
					if res.Code != http.StatusRequestEntityTooLarge {
						t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
					}
					var response problem.Response
					if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.Type != "/problems/content-too-large" || response.Status != 413 || response.RequestID != "emulator-body-limit" || res.Header().Get("Content-Type") != "application/problem+json" || res.Header().Get("X-Request-ID") != "emulator-body-limit" {
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
			handler, err := NewRouter()
			if err != nil {
				t.Fatal(err)
			}
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
			request := "POST /v1/client-overrides/size-client HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-Client-ID: size-client\r\nX-Request-ID: partial-body\r\n" + header + "\r\n" + body
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
