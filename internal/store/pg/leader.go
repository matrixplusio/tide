package pg

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Advisory lock ids for the background loops. Each loop has its own, so they
// can run on different replicas and neither blocks the other.
const (
	LockExecutor = 7419002
	LockCIIntake = 7419003
)

// Lead calls work on every tick, but only while this replica holds the
// advisory lock. Replicas that do not hold it retry every interval.
//
// The lock lives on the same connection the work uses. That is the point:
// "holding the right to act" and "being able to act" are then the same
// thing, and a broken connection ends both at once. A lease in another
// system can outlive the ability to use it — a long pause can let the lease
// expire while the holder still believes it leads, and two leaders act at
// the same time. PostgreSQL releases a session lock when the session ends,
// with no timer and no assumption about clocks.
func Lead(ctx context.Context, db *gorm.DB, lockID int64, interval time.Duration, name string, work func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := lead(ctx, db, lockID, t, name, work); err != nil && ctx.Err() == nil {
			zap.L().Warn("leadership lost", zap.String("loop", name), zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func lead(ctx context.Context, db *gorm.DB, lockID int64, t *time.Ticker, name string, work func(context.Context)) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		var ok bool
		if err := conn.Raw(`SELECT pg_try_advisory_lock($1)`, lockID).Scan(&ok).Error; err != nil || !ok {
			return err
		}
		zap.L().Info("acquired leadership", zap.String("loop", name))
		// Releasing is the last thing this replica does, and it happens
		// precisely because ctx was cancelled; using ctx would skip it and
		// leave the lock held until the connection drops.
		defer conn.WithContext(context.Background()).Exec(`SELECT pg_advisory_unlock($1)`, lockID) //nolint:contextcheck // see above
		for {
			// Proves the connection — and therefore the lock — is still there
			// before doing anything that assumes leadership.
			if err := conn.Exec(`SELECT 1`).Error; err != nil {
				return err
			}
			work(ctx)
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	})
}
