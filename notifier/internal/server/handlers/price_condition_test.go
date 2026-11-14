package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestCreateNotificationPriceCondition(t *testing.T) {
	client := core.Client{ID: "price-client", MaxLifetime: 24 * time.Hour}
	const requestID = "price-request"

	tests := []struct {
		name      string
		condition string
		want      *core.PriceCondition
	}{
		{
			name:      "greater than zero",
			condition: `"operation":"greater_than","value":0`,
			want:      &core.PriceCondition{Operation: core.GreaterThan, Threshold: 0},
		},
		{
			name:      "less than",
			condition: `"operation":"less_than","value":19990`,
			want:      &core.PriceCondition{Operation: core.LessThan, Threshold: 19990},
		},
		{"negative", `"operation":"greater_than","value":-1`, nil},
		{"fractional", `"operation":"less_than","value":1.5`, nil},
		{"string", `"operation":"less_than","value":"10"`, nil},
		{"overflow", `"operation":"less_than","value":9223372036854775808`, nil},
		{"missing threshold", `"operation":"greater_than"`, nil},
		{"null threshold", `"operation":"greater_than","value":null`, nil},
		{"unknown operation", `"operation":"equal","value":0`, nil},
		{"extra field", `"operation":"greater_than","value":0,"currency":"USD"`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &notificationStoreStub{id: uuid.New()}
			server, err := codegen.NewServer(
				NewHandler(store, time.Second),
				middleware.NewClientSecurity(clientSourceStub{client: client}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}

			body := `{"external_id":"order-42","notification_params":[{"type":"price",` + tt.condition + `}]}`
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Client-ID", client.ID)
			req.Header.Set("X-Request-ID", requestID)
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if got := resp.Header().Get("X-Request-ID"); got != requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, requestID)
			}
			if tt.want != nil {
				if resp.Code != http.StatusOK || len(store.saved) != 1 {
					t.Fatalf("HTTP %d with %s and %d writes, want HTTP 200 and one write", resp.Code, resp.Body, len(store.saved))
				}
				conditions := store.saved[0].Params
				if len(conditions) != 1 || conditions[0].Type != core.ConditionPrice || conditions[0].Price != *tt.want {
					t.Errorf("stored conditions = %+v, want price %+v", conditions, *tt.want)
				}
				return
			}

			if resp.Code != http.StatusBadRequest || len(store.saved) != 0 {
				t.Fatalf("HTTP %d with %s and %d writes, want HTTP 400 and no writes", resp.Code, resp.Body, len(store.saved))
			}
			if resp.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("unexpected content type: %v", resp.Header())
			}
			var problem codegen.Problem
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Type != "/problems/validation-error" || problem.Status != http.StatusBadRequest {
				t.Errorf("unexpected problem: %+v", problem)
			}
		})
	}
}
