package activity

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/config"
)

// Retention defaults. created_at (server write time) is the retention
// authority, not occurred_at — a client with a skewed clock cannot place its
// rows outside the retention window.
const (
	defaultRetentionDays = 180
	sweepInterval        = 24 * time.Hour
	// deleteBatchSize bounds one DELETE so the sweep never holds a long
	// row-lock window on a table the ingest path is writing to.
	deleteBatchSize = 5000
)

// StartRetention launches the daily user_activity retention sweep and
// returns a channel closed when the goroutine exits. Follows the
// opsmetrics.Start contract so main.go can join it during shutdown.
// ACTIVITY_RETENTION_DAYS <= 0 disables the sweep (keep everything).
func StartRetention(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	days := config.GetEnvInt("ACTIVITY_RETENTION_DAYS", defaultRetentionDays)

	go func() {
		defer close(done)
		if days <= 0 {
			slog.Info("activity retention disabled", "ACTIVITY_RETENTION_DAYS", days)
			return
		}
		// First sweep shortly after boot rather than a day later, but off the
		// critical startup path.
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				sweep(ctx, db, days)
				timer.Reset(sweepInterval)
			}
		}
	}()
	return done
}

// sweep deletes expired rows in bounded batches until none remain or the
// context is cancelled.
func sweep(ctx context.Context, db *gorm.DB, days int) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	total := int64(0)
	for {
		if ctx.Err() != nil {
			return
		}
		res := db.WithContext(ctx).Exec(
			`DELETE FROM user_activity WHERE id IN
			 (SELECT id FROM user_activity WHERE created_at < ? LIMIT ?)`,
			cutoff, deleteBatchSize,
		)
		if res.Error != nil {
			slog.Error("activity retention sweep failed", "error", res.Error)
			return
		}
		total += res.RowsAffected
		if res.RowsAffected < deleteBatchSize {
			break
		}
	}
	if total > 0 {
		slog.Info("activity retention sweep completed", "deleted", total, "retentionDays", days)
	}
}
