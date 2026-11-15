package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
					bodylimit.Requests(middleware.RequestTimeout(time.Second, time.Second, router)).ServeHTTP(res, req)
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
			handler := bodylimit.Requests(middleware.RequestTimeout(5*time.Second, time.Second, router))
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

func TestCompleteOversizedChunkedBodyPreserves413AfterReadCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const requestID = "complete-oversized-body"
	const payload = `{"external_id":"size-check","notification_params":[{"type":"price","operation":"greater_than","value":100}]}`
	body := payload + strings.Repeat(" ", int(bodylimit.Bytes)-len(payload))
	request := "POST /v1/notifications HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-Client-ID: size-client\r\nX-Request-ID: " + requestID + "\r\nTransfer-Encoding: chunked\r\n\r\n" +
		fmt.Sprintf("%x\r\n%s\r\n1\r\n \r\n0\r\n\r\n", len(body), body)
	store := &notificationStoreStub{id: uuid.New()}
	router, err := codegen.NewServer(NewHandler(store, time.Second), middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: "size-client", MaxLifetime: time.Hour}}), codegen.WithErrorHandler(middleware.HandleError))
	if err != nil {
		t.Fatal(err)
	}
	requestContexts := make(chan context.Context, 1)
	handler := bodylimit.Requests(middleware.RequestTimeout(3*time.Second, time.Second, router))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestContexts <- r.Context()
		handler.ServeHTTP(w, r)
	}))
	listener := &readCancellationListener{
		Listener: server.Listener, ctx: ctx, requestSize: len(request), requestContexts: requestContexts,
		cancellation: make(chan error, 1),
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-listener.cancellation:
		if err != nil {
			t.Fatalf("force background-read cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("read deadline did not cancel the background read")
	}
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %q, want 413 after read cancellation", response.StatusCode, data)
	}
	var result problem.Response
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Type != "/problems/content-too-large" || result.Status != 413 || result.RequestID != requestID ||
		response.Header.Get("Content-Type") != "application/problem+json" || response.Header.Get("X-Request-ID") != requestID || !response.Close {
		t.Fatalf("unexpected problem: %+v, headers: %v, close: %v", result, response.Header, response.Close)
	}
	if len(store.saved) != 0 {
		t.Fatal("oversized body reached storage")
	}
}

type readCancellationListener struct {
	net.Listener
	ctx             context.Context
	requestSize     int
	requestContexts <-chan context.Context
	cancellation    chan error
}

func (l *readCancellationListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &readCancellationConn{
		Conn: conn, listener: l, backgroundRead: make(chan struct{}),
	}, nil
}

type readCancellationConn struct {
	net.Conn
	listener       *readCancellationListener
	request        *strings.Reader
	backgroundRead chan struct{}
	backgroundOnce sync.Once
	cancelOnce     sync.Once
}

func (c *readCancellationConn) Read(p []byte) (int, error) {
	if c.request == nil {
		// DND: Keep the final chunk and EOF buffered together; packet arrival order
		// must not decide whether net/http starts the background read under test.
		request := make([]byte, c.listener.requestSize)
		if _, err := io.ReadFull(c.Conn, request); err != nil {
			return 0, err
		}
		c.request = strings.NewReader(string(request))
	}
	if c.request.Len() > 0 {
		return c.request.Read(p)
	}
	c.backgroundOnce.Do(func() { close(c.backgroundRead) })
	return c.Conn.Read(p)
}

func (c *readCancellationConn) SetReadDeadline(deadline time.Time) error {
	if deadline.IsZero() || deadline.After(time.Now()) {
		return c.Conn.SetReadDeadline(deadline)
	}
	var cancellationErr error
	// DND: Force cancellation before returning the expired deadline call so the old
	// response-loss race cannot pass just because the handler wins the scheduling race.
	c.cancelOnce.Do(func() {
		cancellationErr = c.cancelBackgroundRead(deadline)
		c.listener.cancellation <- cancellationErr
	})
	return errors.Join(cancellationErr, c.Conn.SetReadDeadline(deadline))
}

func (c *readCancellationConn) cancelBackgroundRead(deadline time.Time) error {
	select {
	case <-c.backgroundRead:
	case <-c.listener.ctx.Done():
		return errors.New("background read did not start after chunked EOF")
	}
	if err := c.Conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	select {
	case requestContext := <-c.listener.requestContexts:
		select {
		case <-requestContext.Done():
			if !errors.Is(requestContext.Err(), context.Canceled) {
				return requestContext.Err()
			}
			return nil
		case <-c.listener.ctx.Done():
			return errors.New("request context was not canceled by the read timeout")
		}
	case <-c.listener.ctx.Done():
		return errors.New("request handler did not start")
	}
}
