//go:build darwin || linux

package main

import "testing"

func TestCollectorProcessExitsAfterSIGTERMWithBlockedPool(t *testing.T) {
	config := "collector:\n  batch_size: 1000\n  retention_days: 365\ndatabase:\n  min_conns: 2\n  max_conns: 2\n  operation_timeout_sec: 6\nnotification:\n  scheduled_time_sec: 1\n"
	testProcessExitsAfterSIGTERMWithBlockedPool(t, "collector", config)
}
