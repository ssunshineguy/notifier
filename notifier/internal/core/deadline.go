package core

import "time"

func checkDeadline(deadline, now, maxDeadline time.Time) []FieldError {
	var errs []FieldError
	add := func(msg string) {
		errs = append(errs, FieldError{Field: "/deadline_dt", Message: msg})
	}
	if !deadline.After(now) {
		add("must be in the future")
	}
	if deadline.After(maxDeadline) {
		add("exceeds the maximum notification lifetime for the client")
	}
	return errs
}
