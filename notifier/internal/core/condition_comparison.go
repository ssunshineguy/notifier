package core

// ComparisonOperation selects which side of a threshold satisfies a condition.
type ComparisonOperation string

const (
	// GreaterThan excludes values at or below the threshold.
	GreaterThan ComparisonOperation = "greater_than"
	// LessThan excludes values at or above the threshold.
	LessThan ComparisonOperation = "less_than"
)

// IsValid reports whether the operation is a supported strict comparison.
func (op ComparisonOperation) IsValid() bool {
	return op == GreaterThan || op == LessThan
}
