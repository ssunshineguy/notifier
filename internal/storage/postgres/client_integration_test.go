//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/storage/postgres"
)

func TestGetClientIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	ctx := t.Context()
	createdAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	const callbackURL = "https://example.test/notify"

	_, err := pool.Exec(ctx, `
		INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec, callback_timeout_ms, created_dt)
		VALUES ('default-timeout', $1, 7200, NULL, $2), ('custom-timeout', $1, 7200, 350, $2)`, callbackURL, createdAt)
	if err != nil {
		t.Fatal(err)
	}

	for _, clientID := range []string{"default-timeout", "custom-timeout"} {
		t.Run(clientID, func(t *testing.T) {
			client, err := store.GetClient(t.Context(), clientID)
			if err != nil {
				t.Fatal(err)
			}
			if client.ID != clientID || client.CallbackURL != callbackURL ||
				client.MaxLifetime != 2*time.Hour || !client.CreatedAt.Equal(createdAt) {
				t.Fatalf("unexpected client: %+v", client)
			}
			if clientID == "default-timeout" {
				if client.CallbackTimeout != nil {
					t.Errorf("callback timeout = %v, want nil", *client.CallbackTimeout)
				}
				return
			}
			if client.CallbackTimeout == nil {
				t.Fatal("callback timeout is missing")
			}
			if *client.CallbackTimeout != 350*time.Millisecond {
				t.Errorf("callback timeout = %v, want 350ms", *client.CallbackTimeout)
			}
		})
	}

	_, err = store.GetClient(ctx, "missing")
	if !errors.Is(err, core.ErrClientNotFound) {
		t.Fatalf("GetClient for missing ID = %v, want ErrClientNotFound", err)
	}

	pool.Close()
	_, err = store.GetClient(ctx, "default-timeout")
	if err == nil || errors.Is(err, core.ErrClientNotFound) {
		t.Fatalf("GetClient on a closed pool = %v, want a storage error", err)
	}
}
