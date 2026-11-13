package middleware_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
)

type clientSourceStub struct {
	client     core.Client
	err        error
	receivedID string
}

func (s *clientSourceStub) GetClient(_ context.Context, clientID string) (core.Client, error) {
	s.receivedID = clientID
	return s.client, s.err
}

func TestHandleClientID(t *testing.T) {
	client := core.Client{
		ID:          "client-42",
		CallbackURL: "https://example.test/notify",
		MaxLifetime: time.Hour,
	}
	dbErr := errors.New("database unavailable")

	tests := []struct {
		name   string
		client core.Client
		err    error
	}{
		{name: "client found", client: client},
		{name: "unknown client", err: core.ErrClientNotFound},
		{name: "database error", err: dbErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &clientSourceStub{client: tt.client, err: tt.err}
			security := middleware.NewClientSecurity(source)

			ctx, err := security.HandleClientID(
				t.Context(),
				codegen.CreateNotificationOperation,
				codegen.ClientID{APIKey: client.ID},
			)
			if source.receivedID != client.ID {
				t.Errorf("looked up client ID %q, want %q", source.receivedID, client.ID)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("got error %v, want %v", err, tt.err)
			}

			got, found := middleware.ClientFromContext(ctx)
			wantFound := tt.err == nil
			if found != wantFound || got != tt.client {
				t.Errorf("client in context = (%+v, %t), want (%+v, %t)", got, found, tt.client, wantFound)
			}
		})
	}
}
