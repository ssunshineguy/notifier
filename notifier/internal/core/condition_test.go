package core_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

var (
	now    = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	client = core.Client{ID: "test", MaxLifetime: 180 * 24 * time.Hour}
)

func leaf(after time.Time) core.Condition {
	return core.Condition{Type: core.ConditionTime, After: after}
}

func and(conds ...core.Condition) core.Condition {
	return core.Condition{Type: core.ConditionAnd, Conditions: conds}
}

func or(conds ...core.Condition) core.Condition {
	return core.Condition{Type: core.ConditionOr, Conditions: conds}
}

func leaves(n int, after time.Time) []core.Condition {
	conds := make([]core.Condition, n)
	for i := range conds {
		conds[i] = leaf(after)
	}
	return conds
}

func fieldErrors(t *testing.T, err error) []core.FieldError {
	t.Helper()
	if err == nil {
		return nil
	}
	var v *core.ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("want *core.ValidationError, got %v", err)
	}
	return v.Fields
}

func TestNewNotificationConditionTree(t *testing.T) {
	a := leaf(now.Add(time.Hour))

	tests := []struct {
		name   string
		params []core.Condition
		want   []core.FieldError
	}{
		{
			name:   "single leaf",
			params: []core.Condition{a},
		},
		{
			name:   "three levels of groups",
			params: []core.Condition{a, or(a, and(a, or(a, a)))},
		},
		{
			name:   "four levels of groups",
			params: []core.Condition{or(a, and(a, or(a, and(a, a))))},
			want: []core.FieldError{{
				Field:   "/notification_params/0/conditions/1/conditions/1/conditions/1",
				Message: "groups are nested deeper than 3 levels",
			}},
		},
		{
			name:   "20 leaves",
			params: []core.Condition{and(leaves(10, a.After)...), and(leaves(10, a.After)...)},
		},
		{
			name:   "21 leaves",
			params: []core.Condition{and(leaves(10, a.After)...), and(leaves(10, a.After)...), a},
			want: []core.FieldError{{
				Field:   "/notification_params",
				Message: "has 21 leaf conditions, at most 20 allowed",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := core.CreateRequest{ExternalID: "order-1", Params: tt.params}
			_, err := core.NewNotification(client, req, now, time.Minute)
			if got := fieldErrors(t, err); !slices.Equal(got, tt.want) {
				t.Errorf("field errors:\n got %v\nwant %v", got, tt.want)
			}
		})
	}
}
