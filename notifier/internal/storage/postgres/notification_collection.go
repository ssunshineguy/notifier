package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DeleteStoppedNotifications atomically removes at most limit stopped rows strictly before the cutoff.
func (s *Store) DeleteStoppedNotifications(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, fmt.Errorf("collection limit must be positive")
	}
	var deleted int64
	err := s.scanRow(ctx, "delete_stopped_notifications", deleteStoppedNotifications, []any{before, limit}, func() slog.Attr {
		return slog.Int64("deleted_count", deleted)
	}, &deleted)
	if err != nil {
		return 0, fmt.Errorf("delete stopped notifications: %w", err)
	}
	return deleted, nil
}
