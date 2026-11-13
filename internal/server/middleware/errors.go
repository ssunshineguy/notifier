package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
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

	problem := codegen.Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
	}
	switch status {
	case http.StatusBadRequest:
		problem.Type = "/problems/validation-error"
		problem.Title = "Request validation failed"
	case http.StatusUnauthorized:
		problem.Type = "/problems/unauthorized"
	case http.StatusInternalServerError:
		problem.Type = "/problems/internal-error"
		problem.Title = "Internal server error"
	}
	writeProblem(ctx, w, r, problem)
}

func writeProblem(ctx context.Context, w http.ResponseWriter, r *http.Request, problem codegen.Problem) {
	if requestID := r.Header.Get(requestIDHeader); requestID != "" {
		w.Header().Set(requestIDHeader, requestID)
		problem.RequestID = codegen.NewOptString(requestID)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(&problem); err != nil {
		slog.WarnContext(ctx, "write problem response", "error", err)
	}
}
