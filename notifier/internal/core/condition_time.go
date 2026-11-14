package core

import "time"

// TimeCondition describes a strict comparison against a timestamp.
type TimeCondition struct {
	Operation ComparisonOperation
	Threshold time.Time
}
