package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/conditions"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

// CreateNotification saves the notification after validating it against the client's settings and the condition tree.
func (h *Handler) CreateNotification(ctx context.Context, req *codegen.NotificationCreate, params codegen.CreateNotificationParams) (codegen.CreateNotificationRes, error) {
	client, create, err := prepareCreateNotification(ctx, req)
	if err != nil {
		return nil, err
	}

	logger := logging.FromContext(ctx)
	requestID := codegen.NewOptString(params.XRequestID)
	notification, err := core.NewNotification(client, create, time.Now(), h.scheduledTime)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelInfo, "API Request Parameters",
			logging.Redacted("external_id"), slog.Int("top_level_condition_count", len(req.NotificationParams)), slog.Any("deadline_dt", create.Deadline))

		var validationErr *core.ValidationError
		if !errors.As(err, &validationErr) {
			return nil, err
		}
		logger.InfoContext(ctx, "Notification Condition Checks", "outcome", "failed",
			"invalid_field_count", len(validationErr.Fields))

		id, found, lookupErr := h.store.FindNotificationID(ctx, client.ID, create.ExternalID)
		if lookupErr != nil {
			return internalErrorResponse(requestID), nil
		}
		if found {
			return alreadyExistsResponse(requestID, id), nil
		}
		return validationResponse(requestID, validationErr.Fields), nil
	}

	logger.LogAttrs(ctx, slog.LevelInfo, "API Request Parameters",
		logging.Redacted("external_id"), slog.Any("notification_params", req.NotificationParams), slog.Any("deadline_dt", create.Deadline))
	logger.InfoContext(ctx, "Notification Condition Checks", "outcome", "passed")

	id, err := h.store.CreateNotification(ctx, notification)
	if err != nil {
		var existsErr *core.AlreadyExistsError
		if errors.As(err, &existsErr) {
			return alreadyExistsResponse(requestID, existsErr.ID), nil
		}
		if errors.Is(err, core.ErrNotificationDuplicateDisappeared) {
			return &codegen.ProblemHeaders{
				XRequestID: requestID,
				Response: codegen.Problem{
					Type:      "/problems/notification-duplicate-disappeared",
					Title:     "Duplicate notification was concurrently deleted",
					Status:    http.StatusConflict,
					Detail:    codegen.NewOptString("The conflicting notification was deleted before its identifier could be read. Retry creation if it is still relevant."),
					RequestID: requestID,
				},
			}, nil
		}
		return internalErrorResponse(requestID), nil
	}
	return &codegen.NotificationCreatedHeaders{
		XRequestID: requestID,
		Response:   codegen.NotificationCreated{NotificationID: codegen.NotificationId(id)},
	}, nil
}

func alreadyExistsResponse(requestID codegen.OptString, id uuid.UUID) *codegen.AlreadyExistsApplicationJSONHeaders {
	return &codegen.AlreadyExistsApplicationJSONHeaders{
		XRequestID: requestID,
		Response:   codegen.NotificationCreated{NotificationID: codegen.NotificationId(id)},
	}
}

func prepareCreateNotification(ctx context.Context, req *codegen.NotificationCreate) (core.Client, core.CreateRequest, error) {
	client, ok := middleware.ClientFromContext(ctx)
	if !ok {
		return core.Client{}, core.CreateRequest{}, errors.New("client missing from request context")
	}

	params := conditions.Tree(req.NotificationParams)
	if !params.CanEncode() {
		return core.Client{}, core.CreateRequest{}, errors.New("unencodable notification conditions")
	}

	create := core.CreateRequest{
		ExternalID: string(req.ExternalID),
		Params:     params,
	}
	if deadline, ok := req.DeadlineDt.Get(); ok {
		create.Deadline = &deadline
	}

	return client, create, nil
}
