// Package infoservice reads time and price from the Information Service API.
package infoservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/infoservice/codegen"
)

// Client applies an operation-specific deadline to outbound requests.
type Client struct {
	client       *codegen.Client
	timeTimeout  time.Duration
	priceTimeout time.Duration
}

// NewClient builds an Information Service client using the configured address and request budgets.
func NewClient(settings config.InfoService) (*Client, error) {
	httpClient := &http.Client{
		Transport: responseLimitTransport{base: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	client, err := codegen.NewClient(settings.BaseURL, codegen.WithClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create information service client: %w", err)
	}
	return &Client{
		client:       client,
		timeTimeout:  time.Duration(settings.TimeTimeoutMS) * time.Millisecond,
		priceTimeout: time.Duration(settings.PriceTimeoutMS) * time.Millisecond,
	}, nil
}

// GetTime returns the clock value for the given client.
func (c *Client) GetTime(ctx context.Context, clientID, requestID string) (time.Time, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.timeTimeout)
	defer cancel()

	response, err := c.client.GetTime(requestCtx, codegen.GetTimeParams{
		XClientID: clientID, XRequestID: requestID,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("get information service time: %w", err)
	}
	switch response := response.(type) {
	case *codegen.TimeHeaders:
		return response.Response.Time, nil
	case *codegen.BadInformationRequestHeaders:
		return time.Time{}, errors.New("get information service time: HTTP 400")
	default:
		return time.Time{}, fmt.Errorf("get information service time: unexpected response %T", response)
	}
}

// GetPrice returns the price in minor units for the given client.
func (c *Client) GetPrice(ctx context.Context, clientID, requestID string) (int64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.priceTimeout)
	defer cancel()

	response, err := c.client.GetPrice(requestCtx, codegen.GetPriceParams{
		XClientID: clientID, XRequestID: requestID,
	})
	if err != nil {
		return 0, fmt.Errorf("get information service price: %w", err)
	}
	switch response := response.(type) {
	case *codegen.PriceHeaders:
		return response.Response.Price, nil
	case *codegen.BadInformationRequestHeaders:
		return 0, errors.New("get information service price: HTTP 400")
	default:
		return 0, fmt.Errorf("get information service price: unexpected response %T", response)
	}
}
