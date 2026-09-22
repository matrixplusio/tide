// Package pg is Tide's data access: hand-written SQL through gorm Raw/Exec.
// Every repository is bound to a *gorm.DB that is either the pool or a
// transaction, so writes and their audit records commit together.
package pg

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

type Store struct {
	db       *gorm.DB
	Audit    *Audit
	Releases *Releases
	Accounts *Accounts
	Access   *Access
	Settings *Settings
	CI       *CI
	Cache    *Cache
	Insights *Insights
}

func New(db *gorm.DB) *Store {
	s := &Store{db: db}
	s.Audit = &Audit{db: db}
	s.Releases = &Releases{db: db, audit: s.Audit}
	s.Accounts = &Accounts{db: db}
	s.Access = &Access{db: db}
	s.Settings = &Settings{db: db}
	s.CI = &CI{db: db}
	s.Cache = &Cache{db: db}
	s.Insights = &Insights{db: db}
	return s
}

// DB exposes the handle for infrastructure (advisory locks, health checks).
func (s *Store) DB() *gorm.DB { return s.db }

// Tx runs fn with every repository bound to one transaction. Nested calls
// become savepoints.
func (s *Store) Tx(ctx context.Context, fn func(tx *Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(New(tx)) })
}

func (s *Store) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// likePattern escapes LIKE metacharacters in user input and wraps it for a
// substring match (used with ESCAPE '\').
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}
