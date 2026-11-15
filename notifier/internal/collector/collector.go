// Package collector removes expired stopped notifications in bounded transactions.
package collector

import (
	"context"
	"fmt"
	"time"
)

// Store deletes one atomic batch and returns the number of removed notifications.
type Store interface {
	DeleteStoppedNotifications(context.Context, time.Time, int) (int64, error)
}

// Run deletes batches sequentially until storage returns zero, keeping the cutoff fixed.
func Run(ctx context.Context, store Store, batchSize int, before time.Time) (int64, error) {
	if batchSize < 1 {
		return 0, fmt.Errorf("collector batch size must be more than 0")
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		deleted, err := store.DeleteStoppedNotifications(ctx, before, batchSize)
		if err != nil {
			return total, fmt.Errorf("collect stopped notifications: %w", err)
		}
		total += deleted
		if deleted == 0 {
			return total, nil
		}
	}
}
