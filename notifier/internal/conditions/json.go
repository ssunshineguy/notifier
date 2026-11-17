// Package conditions owns the JSON representation shared by HTTP and PostgreSQL.
package conditions

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-faster/jx"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// Tree is the JSON array of notification conditions.
type Tree []core.Condition

type node struct {
	Type       core.ConditionType       `json:"type"`
	Operation  core.ComparisonOperation `json:"operation,omitempty"`
	Value      any                      `json:"value,omitempty"`
	Conditions *Tree                    `json:"conditions,omitempty"`
}

// MarshalJSON encodes the same condition values for HTTP and PostgreSQL.
func (t Tree) MarshalJSON() ([]byte, error) {
	nodes := make([]node, len(t))
	for i, condition := range t {
		item := &nodes[i]
		item.Type = condition.Type
		switch condition.Type {
		case core.ConditionTime:
			item.Operation = condition.Time.Operation
			item.Value = secondPrecision(condition.Time.Threshold)
		case core.ConditionPrice:
			item.Operation = condition.Price.Operation
			item.Value = condition.Price.Threshold
		case core.ConditionAnd, core.ConditionOr:
			children := Tree(condition.Conditions)
			item.Conditions = &children
		default:
			return nil, fmt.Errorf("unsupported condition type %q", condition.Type)
		}
	}
	return json.Marshal(nodes)
}

// UnmarshalJSON decodes the public schema, including required fields and array sizes.
func (t *Tree) UnmarshalJSON(data []byte) error {
	tree, err := decodeTree(jx.DecodeBytes(data), true)
	if err != nil {
		return err
	}
	if !tree.HasValidSizes() {
		return errors.New("invalid condition array size")
	}
	*t = tree
	return nil
}

// DecodeStored reads persisted conditions before the worker checks evaluation limits.
func DecodeStored(data []byte) (Tree, error) {
	return decodeTree(jx.DecodeBytes(data), false)
}

func decodeTree(d *jx.Decoder, strict bool) (Tree, error) {
	tree := make(Tree, 0)
	if !strict && d.Next() == jx.Null {
		return tree, d.Null()
	}
	err := d.Arr(func(d *jx.Decoder) error {
		condition, err := decodeNode(d, strict)
		if err != nil {
			return err
		}
		tree = append(tree, condition)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

func decodeNode(d *jx.Decoder, strict bool) (core.Condition, error) {
	var condition core.Condition
	var operation core.ComparisonOperation
	var value, storedChildren jx.Raw
	var hasOperation, hasChildren bool
	seen := make(map[string]struct{}, 4)
	err := d.Obj(func(d *jx.Decoder, key string) error {
		if strict {
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate condition property")
			}
			seen[key] = struct{}{}
		}
		switch key {
		case "type":
			name, err := d.Str()
			condition.Type = core.ConditionType(name)
			return err
		case "operation":
			hasOperation = true
			if !strict && d.Next() == jx.Null {
				return d.Null()
			}
			name, err := d.Str()
			operation = core.ComparisonOperation(name)
			return err
		case "value":
			var err error
			value, err = d.Raw()
			return err
		case "conditions":
			hasChildren = true
			if !strict {
				var err error
				storedChildren, err = d.Raw()
				return err
			}
			children, err := decodeTree(d, strict)
			condition.Conditions = children
			return err
		default:
			if strict {
				return errors.New("unknown condition property")
			}
			return d.Skip()
		}
	})
	if err != nil {
		return core.Condition{}, err
	}

	switch condition.Type {
	case core.ConditionTime, core.ConditionPrice:
		if !operation.IsValid() || len(value) == 0 || (strict && hasChildren) {
			return core.Condition{}, errors.New("invalid condition leaf")
		}
		condition.Conditions = nil
		threshold := jx.DecodeBytes(value)
		if condition.Type == core.ConditionTime {
			raw, err := threshold.Str()
			if err != nil {
				return core.Condition{}, err
			}
			timestamp, err := time.Parse(time.RFC3339, raw)
			if err != nil || !canEncodeTime(timestamp) {
				return core.Condition{}, errors.New("invalid time threshold")
			}
			condition.Time = core.TimeCondition{Operation: operation, Threshold: timestamp}
		} else {
			price, err := threshold.Int64()
			if err != nil || price < 0 {
				return core.Condition{}, errors.New("invalid price threshold")
			}
			condition.Price = core.PriceCondition{Operation: operation, Threshold: price}
		}
	case core.ConditionAnd, core.ConditionOr:
		if strict && (!hasChildren || hasOperation || len(value) != 0) {
			return core.Condition{}, errors.New("invalid condition group")
		}
		if len(storedChildren) != 0 {
			children, err := decodeTree(jx.DecodeBytes(storedChildren), false)
			if err != nil {
				return core.Condition{}, err
			}
			condition.Conditions = children
		}
		if !hasChildren {
			condition.Conditions = []core.Condition{}
		}
	default:
		return core.Condition{}, fmt.Errorf("unsupported condition type %q", condition.Type)
	}
	return condition, nil
}

// HasValidSizes checks the root and group array bounds defined by the public schema.
func (t Tree) HasValidSizes() bool {
	return len(t) >= 1 && len(t) <= 10 && validGroupSizes(t)
}

func validGroupSizes(conditions []core.Condition) bool {
	for _, condition := range conditions {
		if condition.Type == core.ConditionAnd || condition.Type == core.ConditionOr {
			children := condition.Conditions
			if len(children) < 2 || len(children) > 10 || !validGroupSizes(children) {
				return false
			}
		}
	}
	return true
}

// CanEncode checks that ogen can use the tree without encountering a JSON encoding error.
func (t Tree) CanEncode() bool {
	for _, condition := range t {
		switch condition.Type {
		case core.ConditionTime:
			if !canEncodeTime(condition.Time.Threshold) {
				return false
			}
		case core.ConditionPrice:
		case core.ConditionAnd, core.ConditionOr:
			if !Tree(condition.Conditions).CanEncode() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func canEncodeTime(timestamp time.Time) bool {
	var buffer [64]byte
	_, err := timestamp.AppendText(buffer[:0])
	return err == nil
}

// AtSecondPrecision copies the tree and truncates its time thresholds before storage.
func (t Tree) AtSecondPrecision() Tree {
	normalized := slices.Clone(t)
	for i := range normalized {
		condition := &normalized[i]
		switch condition.Type {
		case core.ConditionTime:
			condition.Time.Threshold = secondPrecision(condition.Time.Threshold)
		case core.ConditionAnd, core.ConditionOr:
			condition.Conditions = Tree(condition.Conditions).AtSecondPrecision()
		}
	}
	return normalized
}

func secondPrecision(timestamp time.Time) time.Time {
	return timestamp.Truncate(time.Second)
}
