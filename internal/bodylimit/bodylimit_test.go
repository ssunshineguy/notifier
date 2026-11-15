package bodylimit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestsRejectsDeclaredOversizeBeforeReading(t *testing.T) {
	body := &countingBody{remaining: Bytes * 10}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", body)
	req.ContentLength = Bytes + 1
	req.Header.Set("X-Request-ID", "oversized")
	res := httptest.NewRecorder()
	Requests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("oversized request reached the decoder")
	})).ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge || body.read != 0 {
		t.Fatalf("status = %d, bytes read = %d", res.Code, body.read)
	}
	if res.Header().Get("Content-Type") != "application/problem+json" || res.Header().Get("X-Request-ID") != "oversized" || res.Header().Get("Connection") != "close" {
		t.Fatalf("unexpected response headers: %v", res.Header())
	}
	if !strings.Contains(res.Body.String(), `"type":"/problems/content-too-large"`) {
		t.Fatalf("unexpected problem: %s", res.Body.String())
	}
}

func TestRequestsBoundsUnknownLengthReadsAndDrain(t *testing.T) {
	body := &countingBody{remaining: Bytes * 10}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", body)
	req.ContentLength = -1
	Requests(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if !IsExceeded(err) || int64(len(data)) != Bytes {
			t.Fatalf("read bytes = %d, error = %v", len(data), err)
		}
		drained, err := io.Copy(io.Discard, r.Body)
		if drained != 0 || !IsExceeded(err) {
			t.Fatalf("drained bytes = %d, error = %v", drained, err)
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
	if body.read != Bytes+1 {
		t.Fatalf("read %d bytes from underlying body, want %d", body.read, Bytes+1)
	}
}

type countingBody struct {
	remaining int64
	read      int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), b.remaining))
	for i := range n {
		p[i] = ' '
	}
	b.remaining -= int64(n)
	b.read += int64(n)
	return n, nil
}

func (*countingBody) Close() error { return nil }
