package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/ssunshineguy/notifier/internal/server/codegen"
)

// LimitInFlight bounds both active and waiting requests and lets health probes through.
func LimitInFlight(maxRequests, maxQueued int, waitTimeout time.Duration, next http.Handler) http.Handler {
	inFlight := make(chan struct{}, maxRequests)
	waiting := make(chan struct{}, maxQueued)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health/live", "/health/ready":
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		if !takeSlot(ctx, inFlight, waiting, waitTimeout) {
			if ctx.Err() == nil {
				writeProblem(ctx, w, r, codegen.Problem{
					Type:   "/problems/too-many-requests",
					Title:  "Too many requests",
					Status: http.StatusTooManyRequests,
				})
			}
			return
		}
		defer func() { <-inFlight }()
		if ctx.Err() == nil {
			next.ServeHTTP(w, r)
		}
	})
}

func takeSlot(ctx context.Context, inFlight, waiting chan struct{}, waitTimeout time.Duration) bool {
	select {
	case inFlight <- struct{}{}:
		return true
	default:
	}
	if waitTimeout <= 0 {
		return false
	}

	select {
	case waiting <- struct{}{}:
		defer func() { <-waiting }()
	default:
		return false
	}

	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	select {
	case inFlight <- struct{}{}:
		if waitCtx.Err() == nil {
			return true
		}
		<-inFlight
	case <-waitCtx.Done():
	}
	return false
}
