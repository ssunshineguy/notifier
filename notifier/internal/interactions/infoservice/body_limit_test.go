package infoservice

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ogen-go/ogen/validate"

	"github.com/ssunshineguy/notifier/internal/bodylimit"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/infoservice/codegen"
)

func TestClientResponseBodyLimit(t *testing.T) {
	for _, operation := range []struct {
		name string
		body string
	}{
		{name: "time", body: `{"time":"2026-12-01T09:00:00.123456789Z"}`},
		{name: "price", body: `{"price":9223372036854775807}`},
	} {
		for _, framing := range []string{"content length", "chunked"} {
			for _, extra := range []int{0, 1} {
				t.Run(operation.name+"/"+framing+"/extra="+strconv.Itoa(extra), func(t *testing.T) {
					payload := operation.body + strings.Repeat(" ", int(bodylimit.Bytes)-len(operation.body)+extra)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/"+operation.name {
							t.Errorf("request path = %q, want /%s", r.URL.Path, operation.name)
						}
						w.Header().Set("Content-Type", "application/json")
						if framing == "content length" {
							w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
						} else {
							w.(http.Flusher).Flush()
						}
						_, _ = io.WriteString(w, payload)
					}))
					defer server.Close()

					client, err := NewClient(config.InfoService{BaseURL: server.URL, TimeTimeoutMS: 2000, PriceTimeoutMS: 2000})
					if err != nil {
						t.Fatal(err)
					}
					if operation.name == "time" {
						var got time.Time
						got, err = client.GetTime(t.Context(), "fixture", "body-limit-test")
						want := time.Date(2026, time.December, 1, 9, 0, 0, 123456789, time.UTC)
						if extra == 0 && !got.Equal(want) {
							t.Errorf("GetTime = %v, want %v", got, want)
						}
					} else {
						var got int64
						got, err = client.GetPrice(t.Context(), "fixture", "body-limit-test")
						if extra == 0 && got != 9223372036854775807 {
							t.Errorf("GetPrice = %d, want MaxInt64", got)
						}
					}
					if extra == 0 {
						if err != nil {
							t.Fatalf("body at the limit failed: %v", err)
						}
						return
					}
					assertResponseBodyLimitError(t, err)
				})
			}
		}
	}
}

func TestClientResponseBodyLimitAfterGzipDecompression(t *testing.T) {
	payload := `{"price":42}` + strings.Repeat(" ", int(bodylimit.Bytes))
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if int64(compressed.Len()) >= bodylimit.Bytes {
		t.Fatalf("compressed fixture is %d bytes, want less than %d", compressed.Len(), bodylimit.Bytes)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Errorf("Accept-Encoding = %q, want gzip", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()

	client, err := NewClient(config.InfoService{BaseURL: server.URL, TimeTimeoutMS: 2000, PriceTimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetPrice(t.Context(), "fixture", "body-limit-test")
	assertResponseBodyLimitError(t, err)
}

func TestClientBoundsResponseReadsAndDeferredDrain(t *testing.T) {
	for _, tt := range []struct {
		name          string
		status        int
		contentType   string
		contentLength int64
		bodyLength    int
		wantRead      int
		wantSizeError bool
		wantStatus    int
	}{
		{
			name: "content length rejected without reading", status: http.StatusOK, contentType: "application/json",
			contentLength: bodylimit.Bytes + 1, bodyLength: int(bodylimit.Bytes) + 1, wantSizeError: true,
		},
		{
			name: "decoder and deferred drain", status: http.StatusOK, contentType: "application/json",
			contentLength: -1, bodyLength: int(bodylimit.Bytes) * 3, wantRead: int(bodylimit.Bytes) + 1, wantSizeError: true,
		},
		{
			name: "unexpected status drain", status: http.StatusServiceUnavailable, contentType: "text/plain",
			contentLength: -1, bodyLength: int(bodylimit.Bytes) * 3, wantRead: int(bodylimit.Bytes) + 1, wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "unsupported content type drain", status: http.StatusOK, contentType: "text/plain",
			contentLength: -1, bodyLength: int(bodylimit.Bytes) * 3, wantRead: int(bodylimit.Bytes) + 1,
		},
		{
			name: "invalid JSON closes body", status: http.StatusOK, contentType: "application/json",
			contentLength: -1, bodyLength: int(bodylimit.Bytes), wantRead: int(bodylimit.Bytes),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingResponseBody{reader: strings.NewReader(strings.Repeat("{", tt.bodyLength))}
			transport := responseLimitTransport{base: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.status, Header: http.Header{"Content-Type": []string{tt.contentType}},
					Body: body, ContentLength: tt.contentLength, Request: r,
				}, nil
			})}
			generated, err := codegen.NewClient("http://emulator", codegen.WithClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			client := &Client{client: generated, timeTimeout: time.Second, priceTimeout: time.Second}
			_, err = client.GetPrice(t.Context(), "fixture", "body-limit-test")
			if err == nil {
				t.Fatal("GetPrice succeeded with an invalid upstream response")
			}
			if tt.wantSizeError {
				assertResponseBodyLimitError(t, err)
			}
			if tt.wantStatus != 0 {
				var statusError *validate.UnexpectedStatusCodeError
				if !errors.As(err, &statusError) || statusError.StatusCode != tt.wantStatus {
					t.Errorf("GetPrice error = %v, want status %d", err, tt.wantStatus)
				}
			}
			if body.read != tt.wantRead {
				t.Errorf("read %d upstream bytes, want %d including deferred drain", body.read, tt.wantRead)
			}
			if body.closed != 1 {
				t.Errorf("body closed %d times, want 1", body.closed)
			}
		})
	}
}

func assertResponseBodyLimitError(t *testing.T, err error) {
	t.Helper()
	var sizeError *http.MaxBytesError
	if !errors.As(err, &sizeError) || sizeError.Limit != bodylimit.Bytes {
		t.Fatalf("error = %v, want MaxBytesError with limit %d", err, bodylimit.Bytes)
	}
}

type countingResponseBody struct {
	reader *strings.Reader
	read   int
	closed int
}

func (b *countingResponseBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countingResponseBody) Close() error {
	b.closed++
	return nil
}
