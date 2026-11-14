package infoservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ogen-go/ogen/validate"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/infoservice/codegen"
)

func TestClientReadsTimeAndPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Client-ID") != "fixture" || r.Header.Get("X-Request-ID") != "test-request" {
			t.Error("expected GET with the client and request IDs")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/time":
			_, _ = w.Write([]byte(`{"time":"2026-12-01T09:00:00.123456789Z"}`))
		case "/price":
			_, _ = w.Write([]byte(`{"price":9007199254740993}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(config.InfoService{BaseURL: server.URL, TimeTimeoutMS: 2000, PriceTimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	clock, err := client.GetTime(t.Context(), "fixture", "test-request")
	wantTime := time.Date(2026, time.December, 1, 9, 0, 0, 123456789, time.UTC)
	if err != nil || !clock.Equal(wantTime) {
		t.Errorf("GetTime = %v, %v, want %v", clock, err, wantTime)
	}
	price, err := client.GetPrice(t.Context(), "fixture", "test-request")
	const wantPrice = 9007199254740993
	if err != nil || price != wantPrice {
		t.Errorf("GetPrice = %v, %v, want %v", price, err, wantPrice)
	}
}

func TestClientReturnsHTTP400AsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"/problems/validation-error","title":"Request validation failed","status":400}`))
	}))
	defer server.Close()

	client, err := NewClient(config.InfoService{BaseURL: server.URL, TimeTimeoutMS: 2000, PriceTimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetTime(t.Context(), "", "test-request"); err == nil || err.Error() != "get information service time: HTTP 400" {
		t.Errorf("GetTime error = %v, want HTTP 400", err)
	}
	if _, err := client.GetPrice(t.Context(), "", "test-request"); err == nil || err.Error() != "get information service price: HTTP 400" {
		t.Errorf("GetPrice error = %v, want HTTP 400", err)
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("client followed the redirect")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client, err := NewClient(config.InfoService{BaseURL: server.URL, TimeTimeoutMS: 2000, PriceTimeoutMS: 2000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetTime(t.Context(), "fixture", "test-request")
	var statusErr *validate.UnexpectedStatusCodeError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("GetTime error = %v, want HTTP 307", err)
	}
}

func TestClientTimeouts(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		parentTimeout time.Duration
		want          time.Duration
	}{
		{name: "configured time timeout", path: "/time", want: 150 * time.Millisecond},
		{name: "configured price timeout", path: "/price", want: 350 * time.Millisecond},
		{name: "earlier parent deadline", path: "/price", parentTimeout: 50 * time.Millisecond, want: 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const baseURL = "http://emulator"
				client, err := NewClient(config.InfoService{BaseURL: baseURL, TimeTimeoutMS: 150, PriceTimeoutMS: 350})
				if err != nil {
					t.Fatal(err)
				}

				start := time.Now()
				transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if r.URL.Path != tt.path || !ok || deadline.Sub(start) != tt.want {
						t.Errorf("request path = %q, deadline = %v after start, want %q and %v", r.URL.Path, deadline.Sub(start), tt.path, tt.want)
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				client.client, err = codegen.NewClient(baseURL, codegen.WithClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}

				ctx := t.Context()
				if tt.parentTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.parentTimeout)
					defer cancel()
				}
				if tt.path == "/time" {
					_, err = client.GetTime(ctx, "fixture", "test-request")
				} else {
					_, err = client.GetPrice(ctx, "fixture", "test-request")
				}
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != tt.want {
					t.Errorf("request error = %v after %v, want deadline exceeded after %v", err, time.Since(start), tt.want)
				}
			})
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
