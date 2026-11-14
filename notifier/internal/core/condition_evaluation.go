package core

import (
	"cmp"
	"time"
)

// ConditionValues carries the values and errors returned by the time and price sources.
type ConditionValues struct {
	Time     time.Time
	TimeErr  error
	Price    int64
	PriceErr error
}

// ConditionsMet reports whether all top-level conditions hold, returning an error only when a missing value could change the result.
func ConditionsMet(conditions []Condition, values ConditionValues) (bool, error) {
	if len(conditions) == 0 {
		return false, nil
	}
	var unavailable error
	for _, condition := range conditions {
		met, err := conditionMet(condition, values)
		if err != nil {
			unavailable = err
			continue
		}
		if !met {
			return false, nil
		}
	}
	return unavailable == nil, unavailable
}

func conditionMet(condition Condition, values ConditionValues) (bool, error) {
	switch condition.Type {
	case ConditionTime:
		if values.TimeErr != nil {
			return false, values.TimeErr
		}
		return comparisonMet(condition.Time.Operation, values.Time.Compare(condition.Time.Threshold)), nil
	case ConditionPrice:
		if values.PriceErr != nil {
			return false, values.PriceErr
		}
		return comparisonMet(condition.Price.Operation, cmp.Compare(values.Price, condition.Price.Threshold)), nil
	case ConditionAnd:
		return ConditionsMet(condition.Conditions, values)
	case ConditionOr:
		var unavailable error
		for _, child := range condition.Conditions {
			met, err := conditionMet(child, values)
			if err != nil {
				unavailable = err
				continue
			}
			if met {
				return true, nil
			}
		}
		return false, unavailable
	}
	return false, nil
}

func comparisonMet(operation ComparisonOperation, order int) bool {
	return (operation == GreaterThan && order > 0) || (operation == LessThan && order < 0)
}
