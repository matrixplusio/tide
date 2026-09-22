// Package migration applies the SQL files in sql/ in order. Migrations are the
// only source of schema; there is no AutoMigrate.
package migration

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gorm.io/gorm"
)

//go:embed sql/*.sql
var files embed.FS

// AppRole is the runtime account. It gets only INSERT/SELECT on audit_log.
const AppRole = "tide_app"

// Migrate must run as the schema owner, never as AppRole: a table owner can
// always grant itself UPDATE/DELETE back, which would void append-only audit.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		var user string
		if err := conn.Raw(`SELECT current_user`).Scan(&user).Error; err != nil {
			return err
		}
		if user == AppRole {
			return fmt.Errorf("migrate must not run as %s", AppRole)
		}
		if err := conn.Exec(`SELECT pg_advisory_lock(7419001)`).Error; err != nil {
			return err
		}
		defer conn.Exec(`SELECT pg_advisory_unlock(7419001)`)

		if err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`).Error; err != nil {
			return err
		}
		names, err := fs.Glob(files, "sql/*.sql")
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			version := strings.TrimSuffix(strings.TrimPrefix(name, "sql/"), ".sql")
			var n int64
			if err := conn.Raw(`SELECT count(*) FROM schema_migrations WHERE version = $1`, version).Scan(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			body, err := files.ReadFile(name)
			if err != nil {
				return err
			}
			if err := conn.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(string(body)).Error; err != nil {
					return fmt.Errorf("migration %s: %w", version, err)
				}
				return tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, version).Error
			}); err != nil {
				return err
			}
		}
		return grant(conn)
	})
}

func grant(db *gorm.DB) error {
	stmts := []string{
		`GRANT USAGE ON SCHEMA public TO ` + AppRole,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON releases, release_items, release_counters, settings, sessions, captcha_challenges, users, local_credentials, groups, group_members, roles, role_bindings, ci_tokens, ci_intake, cache_generation TO ` + AppRole,
		`GRANT SELECT ON schema_migrations TO ` + AppRole,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + AppRole,
		// append-only: enforced by the database, not by the absence of an endpoint.
		`REVOKE ALL ON audit_log FROM ` + AppRole,
		`GRANT INSERT, SELECT ON audit_log TO ` + AppRole,
		`REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM PUBLIC`,
		// approval decisions are a record too: insert and read only.
		`REVOKE ALL ON release_approvals FROM ` + AppRole,
		`GRANT INSERT, SELECT ON release_approvals TO ` + AppRole,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}
