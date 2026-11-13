// Package middleware handles shared HTTP concerns for the Notifier API.
package middleware

import (
	"context"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
)

type clientContextKey struct{}

// ClientSource supplies client settings by ID.
type ClientSource interface {
	GetClient(ctx context.Context, clientID string) (core.Client, error)
}

// ClientSecurity resolves the client identified by the API Gateway header.
type ClientSecurity struct {
	clients ClientSource
}

var _ codegen.SecurityHandler = (*ClientSecurity)(nil)

// NewClientSecurity uses clients for each protected request.
func NewClientSecurity(clients ClientSource) *ClientSecurity {
	return &ClientSecurity{clients: clients}
}

// HandleClientID loads the client once and makes it available to the request handler.
func (s *ClientSecurity) HandleClientID(ctx context.Context, _ codegen.OperationName, header codegen.ClientID) (context.Context, error) {
	client, err := s.clients.GetClient(ctx, header.APIKey)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, clientContextKey{}, client), nil
}

// ClientFromContext retrieves the client resolved for this request, if present.
func ClientFromContext(ctx context.Context) (core.Client, bool) {
	client, ok := ctx.Value(clientContextKey{}).(core.Client)
	return client, ok
}
