package core

import (
	"errors"
	"time"
)

// ErrClientNotFound is returned by every client source for an unknown client ID.
var ErrClientNotFound = errors.New("client not found")

// Client is an API caller with its settings, whichever source they are read from.
type Client struct {
	ID              string
	CallbackURL     string
	MaxLifetime     time.Duration
	CallbackTimeout *time.Duration
	CreatedAt       time.Time
}
