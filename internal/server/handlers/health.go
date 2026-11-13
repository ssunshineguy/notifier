package handlers

import (
	"context"
	"net/http"

	"github.com/ssunshineguy/notifier/internal/server/codegen"
)

// GetLiveness reports that the HTTP server can respond without checking dependencies.
func (*Handler) GetLiveness(context.Context) (codegen.GetLivenessRes, error) {
	return &codegen.Health{Status: codegen.HealthStatusOk}, nil
}

// GetReadiness checks whether the API can execute a query against PostgreSQL.
func (h *Handler) GetReadiness(ctx context.Context) (codegen.GetReadinessRes, error) {
	if err := h.store.CheckConnection(ctx); err != nil {
		return &codegen.ProblemHeaders{
			Response: codegen.Problem{
				Type:   "/problems/not-ready",
				Title:  "Service is not ready",
				Status: http.StatusServiceUnavailable,
				Detail: codegen.NewOptString("database is unreachable"),
			},
		}, nil
	}

	return &codegen.Health{Status: codegen.HealthStatusOk}, nil
}
