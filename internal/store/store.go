// Package store opens the PostgreSQL connection. SQL lives in store/pg and
// schema changes in store/migration (CONVENTIONS.md §1, §7).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func Open(ctx context.Context, url string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{
		Logger:                 zapGorm{slow: 500 * time.Millisecond},
		SkipDefaultTransaction: true,
		DisableAutomaticPing:   false,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pctx); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return db, nil
}

// zapGorm routes gorm's logs to zap. SQL text is never logged: it may carry
// password hashes or encrypted settings as bound values.
type zapGorm struct {
	slow time.Duration
}

func (l zapGorm) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }

func (zapGorm) Info(_ context.Context, msg string, args ...any) {
	zap.L().Debug(fmt.Sprintf(msg, args...))
}

func (zapGorm) Warn(_ context.Context, msg string, args ...any) {
	zap.L().Warn(fmt.Sprintf(msg, args...))
}

func (zapGorm) Error(_ context.Context, msg string, args ...any) {
	zap.L().Error(fmt.Sprintf(msg, args...))
}

func (l zapGorm) Trace(_ context.Context, begin time.Time, _ func() (string, int64), err error) {
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, context.Canceled):
		zap.L().Debug("sql error", zap.Int64("duration_ms", elapsed.Milliseconds()), zap.Error(err))
	case elapsed > l.slow:
		zap.L().Warn("slow sql", zap.Int64("duration_ms", elapsed.Milliseconds()))
	}
}
