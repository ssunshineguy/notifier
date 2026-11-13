// Package handlers adapts Notifier's HTTP operations to its domain logic.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
)

// NotificationStore saves notifications and finds existing IDs by client and external ID.
type NotificationStore interface {
	CreateNotification(ctx context.Context, notification core.Notification) (uuid.UUID, error)
	FindNotificationID(ctx context.Context, clientID, externalID string) (uuid.UUID, bool, error)
}

// Handler implements the Notifier operations defined in OpenAPI.
type Handler struct {
	codegen.UnimplementedHandler
	notifications NotificationStore
	scheduledTime time.Duration
}

var _ codegen.Handler = (*Handler)(nil)

// NewHandler supplies the store and the delay before the first notification check.
func NewHandler(notifications NotificationStore, scheduledTime time.Duration) *Handler {
	return &Handler{notifications: notifications, scheduledTime: scheduledTime}
}

// CreateNotification saves the notification after validating it against the client's settings and the condition tree.
func (h *Handler) CreateNotification(ctx context.Context, req *codegen.NotificationCreate, params codegen.CreateNotificationParams) (codegen.CreateNotificationRes, error) {
	client, create, err := prepareCreateNotification(ctx, req)
	if err != nil {
		return nil, err
	}

	requestID := codegen.NewOptString(params.XRequestID)
	notification, err := core.NewNotification(client, create, time.Now(), h.scheduledTime)
	if err != nil {
		var validationErr *core.ValidationError
		if !errors.As(err, &validationErr) {
			return nil, err
		}

		id, found, lookupErr := h.notifications.FindNotificationID(ctx, client.ID, create.ExternalID)
		if lookupErr != nil {
			return internalErrorResponse(requestID), nil
		}
		if found {
			return alreadyExistsResponse(requestID, id), nil
		}
		return validationResponse(requestID, validationErr.Fields), nil
	}

	id, err := h.notifications.CreateNotification(ctx, notification)
	if err != nil {
		var existsErr *core.AlreadyExistsError
		if errors.As(err, &existsErr) {
			return alreadyExistsResponse(requestID, existsErr.ID), nil
		}
		return internalErrorResponse(requestID), nil
	}
	return &codegen.NotificationCreatedHeaders{
		XRequestID: requestID,
		Response:   codegen.NotificationCreated{NotificationID: codegen.NotificationId(id)},
	}, nil
}

func alreadyExistsResponse(requestID codegen.OptString, id uuid.UUID) *codegen.AlreadyExistsHeaders {
	return &codegen.AlreadyExistsHeaders{
		XRequestID: requestID,
		Response:   codegen.NotificationCreated{NotificationID: codegen.NotificationId(id)},
	}
}

func internalErrorResponse(requestID codegen.OptString) *codegen.InternalErrorHeaders {
	return &codegen.InternalErrorHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/internal-error",
			Title:     "Internal server error",
			Status:    http.StatusInternalServerError,
			RequestID: requestID,
		},
	}
}

func validationResponse(requestID codegen.OptString, validationFields []core.FieldError) *codegen.BadRequestHeaders {
	fields := make([]codegen.FieldError, len(validationFields))
	for i, field := range validationFields {
		fields[i] = codegen.FieldError{Field: field.Field, Message: field.Message}
	}

	return &codegen.BadRequestHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/validation-error",
			Title:     "Request validation failed",
			Status:    http.StatusBadRequest,
			RequestID: requestID,
			Errors:    fields,
		},
	}
}

func prepareCreateNotification(ctx context.Context, req *codegen.NotificationCreate) (core.Client, core.CreateRequest, error) {
	client, ok := middleware.ClientFromContext(ctx)
	if !ok {
		return core.Client{}, core.CreateRequest{}, errors.New("client missing from request context")
	}

	conditions, err := toCoreConditions(req.NotificationParams)
	if err != nil {
		return core.Client{}, core.CreateRequest{}, err
	}

	create := core.CreateRequest{
		ExternalID: string(req.ExternalID),
		Params:     conditions,
	}
	if deadline, ok := req.DeadlineDt.Get(); ok {
		create.Deadline = &deadline
	}

	return client, create, nil
}

func toCoreConditions(conditions []codegen.Condition) ([]core.Condition, error) {
	result := make([]core.Condition, len(conditions))
	for i, condition := range conditions {
		node := &result[i]
		var children []codegen.Condition
		switch condition.Type {
		case codegen.TimeConditionCondition:
			node.Type = core.ConditionTime
			node.After = condition.TimeCondition.Value
			continue
		case codegen.AndConditionCondition:
			node.Type = core.ConditionAnd
			children = condition.AndCondition.Conditions
		case codegen.OrConditionCondition:
			node.Type = core.ConditionOr
			children = condition.OrCondition.Conditions
		default:
			return nil, fmt.Errorf("unsupported condition type %q", condition.Type)
		}

		nested, err := toCoreConditions(children)
		if err != nil {
			return nil, err
		}
		node.Conditions = nested
	}

	return result, nil
}
