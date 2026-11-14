package core

import "time"

func checkDeadline(deadline, now, maxDeadline time.Time, conds []Condition) []FieldError {
	var errs []FieldError
	add := func(msg string) {
		errs = append(errs, FieldError{Field: "/deadline_dt", Message: msg})
	}
	if !deadline.After(now) {
		add("must be in the future")
	}
	if metAfter, known := timeMetAfter(conds); known && !deadline.After(metAfter) {
		add("must be later than the moment the time conditions can be met, " + metAfter.UTC().Format(time.RFC3339))
	}
	if deadline.After(maxDeadline) {
		add("exceeds the maximum notification lifetime for the client")
	}
	return errs
}
