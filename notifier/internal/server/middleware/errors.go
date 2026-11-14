package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/ssunshineguy/notifier/internal/problem"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const requestIDHeader = "X-Request-ID"

// HandleError converts ogen failures into problem responses without exposing their causes.
func HandleError(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	status := ogenerrors.ErrorCode(err)
	var securityErr *ogenerrors.SecurityError
	if errors.As(err, &securityErr) &&
		!errors.Is(securityErr, ogenerrors.ErrSecurityRequirementIsNotSatisfied) &&
		!errors.Is(securityErr, core.ErrClientNotFound) {
		status = http.StatusInternalServerError
	}

	problem.WriteError(ctx, w, r, status)
}
