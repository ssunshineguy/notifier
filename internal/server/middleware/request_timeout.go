package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
)

// RequestTimeout sets a shared deadline for admission and processing.
func RequestTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started, ok := r.Context().Value(requestStartedKey{}).(time.Time)
		if !ok {
			started = time.Now()
		}
		ctx, cancel := context.WithDeadline(r.Context(), started.Add(timeout))
		defer cancel()

		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
			logging.FromContext(ctx).DebugContext(ctx, "set request body read deadline", "error_type", fmt.Sprintf("%T", err))
		}

		response := &bufferedResponse{header: make(http.Header)}
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			next.ServeHTTP(response, r.WithContext(ctx))
		}()

		select {
		case panicValue := <-done:
			if panicValue != nil {
				panic(panicValue)
			}
			if ctx.Err() == nil {
				response.send(ctx, w)
				return
			}
		case <-ctx.Done():
		}

		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if r.ProtoMajor == 1 {
				w.Header().Set("Connection", "close")
			}
			writeProblem(ctx, w, r, codegen.Problem{
				Type:   "/problems/request-timeout",
				Title:  "Request timed out",
				Status: http.StatusServiceUnavailable,
			})
		}
	})
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *bufferedResponse) Header() http.Header {
	return r.header
}

func (r *bufferedResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *bufferedResponse) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(p)
}

func (r *bufferedResponse) send(ctx context.Context, w http.ResponseWriter) {
	maps.Copy(w.Header(), r.header)
	if r.status != 0 {
		w.WriteHeader(r.status)
	}
	if _, err := r.body.WriteTo(w); err != nil {
		logging.FromContext(ctx).DebugContext(ctx, "write HTTP response", "error_type", fmt.Sprintf("%T", err))
	}
}
