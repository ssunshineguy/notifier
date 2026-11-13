package core

import (
	"fmt"
	"time"
)

const (
	maxGroupDepth = 3
	maxLeafCount  = 20
)

// ConditionType is the kind of a condition tree node.
type ConditionType string

const (
	// ConditionTime becomes true after its configured instant.
	ConditionTime ConditionType = "time"
	// ConditionAnd becomes true when all child conditions hold.
	ConditionAnd ConditionType = "and"
	// ConditionOr becomes true when any child condition holds.
	ConditionOr ConditionType = "or"
)

// Condition is a node of the condition tree: a leaf or an and/or group.
type Condition struct {
	Type ConditionType
	// After is set for time leaves: the leaf is met once the current time is later than After.
	After time.Time
	// Conditions is set for groups.
	Conditions []Condition
}

func (c Condition) isGroup() bool {
	return c.Type == ConditionAnd || c.Type == ConditionOr
}

func checkTree(conds []Condition) []FieldError {
	errs := checkDepth(conds, "/notification_params", 0)
	if n := countLeaves(conds); n > maxLeafCount {
		errs = append(errs, FieldError{
			Field:   "/notification_params",
			Message: fmt.Sprintf("has %d leaf conditions, at most %d allowed", n, maxLeafCount),
		})
	}
	return errs
}

func checkDepth(conds []Condition, path string, depth int) []FieldError {
	var errs []FieldError
	for i, c := range conds {
		if !c.isGroup() {
			continue
		}
		p := fmt.Sprintf("%s/%d", path, i)
		if depth+1 > maxGroupDepth {
			errs = append(errs, FieldError{
				Field:   p,
				Message: fmt.Sprintf("groups are nested deeper than %d levels", maxGroupDepth),
			})
			continue
		}
		errs = append(errs, checkDepth(c.Conditions, p+"/conditions", depth+1)...)
	}
	return errs
}

func countLeaves(conds []Condition) int {
	n := 0
	for _, c := range conds {
		if c.isGroup() {
			n += countLeaves(c.Conditions)
		} else {
			n++
		}
	}
	return n
}
