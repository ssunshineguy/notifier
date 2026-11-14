package core_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

func TestConditionsMet(t *testing.T) {
	threshold := time.Date(2050, time.January, 1, 9, 0, 0, 0, time.UTC)
	const priceThreshold int64 = 9007199254740993
	timeAfter := core.Condition{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: threshold}}
	timeBefore := core.Condition{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.LessThan, Threshold: threshold}}
	priceHigher := core.Condition{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: priceThreshold}}
	priceLower := core.Condition{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: priceThreshold}}
	either := core.Condition{Type: core.ConditionOr, Conditions: []core.Condition{timeBefore, priceHigher}}
	both := core.Condition{Type: core.ConditionAnd, Conditions: []core.Condition{either, priceLower}}

	for _, tt := range []struct {
		name  string
		conds []core.Condition
		time  time.Time
		price int64
		want  bool
	}{
		{"time greater", []core.Condition{timeAfter}, threshold.Add(time.Second), 0, true},
		{"time less", []core.Condition{timeBefore}, threshold.Add(-time.Second), 0, true},
		{"time equals greater threshold", []core.Condition{timeAfter}, threshold, 0, false},
		{"time equals less threshold", []core.Condition{timeBefore}, threshold, 0, false},
		{"price greater", []core.Condition{priceHigher}, threshold, priceThreshold + 1, true},
		{"price less", []core.Condition{priceLower}, threshold, priceThreshold - 1, true},
		{"price equals greater threshold", []core.Condition{priceHigher}, threshold, priceThreshold, false},
		{"price equals less threshold", []core.Condition{priceLower}, threshold, priceThreshold, false},
		{"top-level AND", []core.Condition{timeAfter, priceLower}, threshold.Add(time.Second), priceThreshold, false},
		{"nested groups match", []core.Condition{both}, threshold.Add(-time.Second), priceThreshold - 1, true},
		{"nested AND fails", []core.Condition{both}, threshold.Add(-time.Second), priceThreshold, false},
		{"nested OR fails", []core.Condition{both}, threshold.Add(time.Second), priceThreshold - 1, false},
		{"empty", nil, threshold, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := core.ConditionsMet(tt.conds, core.ConditionValues{Time: tt.time, Price: tt.price})
			if got != tt.want || err != nil {
				t.Errorf("ConditionsMet = (%t, %v), want (%t, nil)", got, err, tt.want)
			}
		})
	}
}

func TestConditionsMetWithUnavailableValues(t *testing.T) {
	threshold := time.Date(2050, time.January, 1, 9, 0, 0, 0, time.UTC)
	timeErr := errors.New("time unavailable")
	priceErr := errors.New("price unavailable")
	timeAfter := core.Condition{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: threshold}}
	priceHigher := core.Condition{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}
	priceLower := core.Condition{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: 5}}
	either := core.Condition{Type: core.ConditionOr, Conditions: []core.Condition{timeAfter, priceHigher}}
	both := core.Condition{Type: core.ConditionAnd, Conditions: []core.Condition{timeAfter, priceHigher}}
	nested := core.Condition{Type: core.ConditionOr, Conditions: []core.Condition{both, priceLower}}

	for _, tt := range []struct {
		name      string
		condition core.Condition
		values    core.ConditionValues
		wantMet   bool
		wantErr   error
	}{
		{name: "OR true after unavailable time", condition: either, values: core.ConditionValues{TimeErr: timeErr, Price: 11}, wantMet: true},
		{name: "OR true with unavailable price", condition: either, values: core.ConditionValues{Time: threshold.Add(time.Second), PriceErr: priceErr}, wantMet: true},
		{name: "OR still needs time", condition: either, values: core.ConditionValues{TimeErr: timeErr, Price: 10}, wantErr: timeErr},
		{name: "OR still needs price", condition: either, values: core.ConditionValues{Time: threshold, Price: 11, PriceErr: priceErr}, wantErr: priceErr},
		{name: "AND still needs time", condition: both, values: core.ConditionValues{TimeErr: timeErr, Price: 11}, wantErr: timeErr},
		{name: "AND false after unavailable time", condition: both, values: core.ConditionValues{TimeErr: timeErr, Price: 10}},
		{name: "OR false after nested AND rules out unavailable time", condition: nested, values: core.ConditionValues{TimeErr: timeErr, Price: 10}},
		{name: "OR true after a nested AND needs time", condition: nested, values: core.ConditionValues{TimeErr: timeErr, Price: 0}, wantMet: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			met, err := core.ConditionsMet([]core.Condition{tt.condition}, tt.values)
			if met != tt.wantMet || !errors.Is(err, tt.wantErr) {
				t.Errorf("ConditionsMet = (%t, %v), want (%t, %v)", met, err, tt.wantMet, tt.wantErr)
			}
		})
	}
}
