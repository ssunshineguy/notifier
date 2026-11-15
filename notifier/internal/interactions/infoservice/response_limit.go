package infoservice

import (
	"net/http"

	"github.com/ssunshineguy/notifier/internal/bodylimit"
)

type responseLimitTransport struct {
	base http.RoundTripper
}

// RoundTrip caps response reads after any decompression performed by the base transport.
func (t responseLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > bodylimit.Bytes {
		_ = response.Body.Close()
		return nil, &http.MaxBytesError{Limit: bodylimit.Bytes}
	}
	// DND: Bound the body itself so ogen's deferred drain cannot bypass the decoder limit.
	response.Body = http.MaxBytesReader(nil, response.Body, bodylimit.Bytes)
	return response, nil
}
