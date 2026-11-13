package core

import "time"

func timeMetAfter(conds []Condition) (moment time.Time, known bool) {
	for _, c := range conds {
		t, ok := timeMetAfterOne(c)
		if !ok {
			continue
		}
		if !known || t.After(moment) {
			moment, known = t, true
		}
	}
	return moment, known
}

func timeMetAfterOne(c Condition) (time.Time, bool) {
	switch c.Type {
	case ConditionTime:
		return c.After, true
	case ConditionAnd:
		return timeMetAfter(c.Conditions)
	case ConditionOr:
		var earliest time.Time
		for i, child := range c.Conditions {
			t, ok := timeMetAfterOne(child)
			if !ok {
				return time.Time{}, false
			}
			if i == 0 || t.Before(earliest) {
				earliest = t
			}
		}
		return earliest, true
	default:
		return time.Time{}, false
	}
}
