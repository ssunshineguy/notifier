package core

// PriceCondition describes a strict comparison against a price threshold.
type PriceCondition struct {
	Operation ComparisonOperation
	Threshold int64
}
