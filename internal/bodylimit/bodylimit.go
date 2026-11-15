// Package bodylimit bounds HTTP JSON bodies before generated decoders read them.
package bodylimit

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/problem"
)

// Bytes is the shared limit for API requests and Information Service responses.
// DND: 20 RFC3339Nano time leaves, at most 19 groups, a 128-character external ID and a deadline
// fit in 3 KiB of compact JSON or 13 KiB with every string and key Unicode-escaped.
// 32 KiB leaves room for formatting; whitespace and timestamp fractions are still
// subject to this byte limit, not an unlimited promise to accept every JSON spelling.
const Bytes int64 = 32 * 1024

// Requests caps decoder reads; install it outside middleware that hides the response controller.
func Requests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopReading := func() {
			err := http.NewResponseController(w).SetReadDeadline(time.Now())
			if err != nil && !errors.Is(err, http.ErrNotSupported) {
				logging.FromContext(r.Context()).DebugContext(r.Context(), "stop oversized request body read", "error_type", fmt.Sprintf("%T", err))
			}
		}
		if r.ContentLength > Bytes {
			defer stopReading()
			problem.WriteError(r.Context(), w, r, http.StatusRequestEntityTooLarge)
			return
		}
		body := &requestBody{ReadCloser: http.MaxBytesReader(w, r.Body, Bytes)}
		r.Body = body
		// DND: Abort the implicit drain only after the response is committed. At chunked EOF,
		// an expired read deadline cancels net/http's background read and the request context;
		// doing this inside Read lets RequestTimeout discard the buffered 413.
		defer func() {
			if body.exceeded.Load() {
				stopReading()
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// IsExceeded recognizes the size error through generated decoder wrapping.
func IsExceeded(err error) bool {
	var sizeError *http.MaxBytesError
	return errors.As(err, &sizeError)
}

type requestBody struct {
	io.ReadCloser
	exceeded atomic.Bool
}

// Read records overflow so Close can leave cleanup until after the response.
func (b *requestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if IsExceeded(err) {
		b.exceeded.Store(true)
	}
	return n, err
}

// Close leaves oversized-body cleanup to net/http after the response has been written.
func (b *requestBody) Close() error {
	// DND: A failed drain cancels the request context; letting ogen close here would
	// cause RequestTimeout to discard the buffered 413 before net/http writes it.
	if b.exceeded.Load() {
		return nil
	}
	return b.ReadCloser.Close()
}
