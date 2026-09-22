package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"tide/internal/store/migration"
)

// Tide cannot create its own roles or database: doing so needs a superuser,
// and holding one would mean a compromised pod owns the whole PostgreSQL
// instance. It prints the statements instead, filled in from the two URLs it
// was configured with, so nobody has to copy a password by hand and get it
// wrong.

// Account is one of the two roles Tide connects as.
type Account struct {
	Role     string
	Password string
	Database string
}

// account reads a connection string without connecting; the database it
// names does not exist yet when this runs.
func account(name, url string) (Account, error) {
	if strings.TrimSpace(url) == "" {
		return Account{}, fmt.Errorf("%s is not set", name)
	}
	cfg, err := pgconn.ParseConfig(url)
	if err != nil {
		return Account{}, fmt.Errorf("%s: %w", name, err)
	}
	switch {
	case cfg.User == "":
		return Account{}, fmt.Errorf("%s has no user", name)
	case cfg.Database == "":
		return Account{}, fmt.Errorf("%s has no database name", name)
	}
	return Account{Role: cfg.User, Password: cfg.Password, Database: cfg.Database}, nil
}

// BootstrapSQL renders the statements a superuser runs once, before Tide
// starts. With withPasswords false the passwords are replaced by a note
// saying where to find them: the same text then goes into a log, which must
// never carry a secret (CONVENTIONS.md §3).
func BootstrapSQL(appURL, migrateURL string, withPasswords bool) (string, error) {
	app, err := account("TIDE_DATABASE_URL", appURL)
	if err != nil {
		return "", err
	}
	owner, err := account("TIDE_DATABASE_MIGRATE_URL", migrateURL)
	if err != nil {
		return "", err
	}
	switch {
	case app.Database != owner.Database:
		return "", fmt.Errorf("the two URLs name different databases (%q and %q); they must be the same database",
			app.Database, owner.Database)
	case app.Role == owner.Role:
		// The whole point of the second role is that the runtime account does
		// not own the tables: an owner can grant itself UPDATE on audit_log
		// whenever it likes, and the audit trail stops being evidence.
		return "", fmt.Errorf("both URLs use the role %q; Tide needs a separate owner role so the runtime account cannot rewrite the audit log", app.Role)
	case app.Role != migration.AppRole:
		return "", fmt.Errorf("TIDE_DATABASE_URL must connect as %q (it is the role the migrations grant append-only access to), not %q",
			migration.AppRole, app.Role)
	}

	pw := func(a Account, envVar string) string {
		if withPasswords {
			return quote(a.Password)
		}
		return "'<the password in " + envVar + ">'"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `-- Run once as a PostgreSQL superuser, before starting Tide.
-- Generated from TIDE_DATABASE_URL and TIDE_DATABASE_MIGRATE_URL, so the
-- passwords here are the ones Tide is configured to use.

CREATE ROLE %s LOGIN PASSWORD %s;
CREATE ROLE %s LOGIN PASSWORD %s;
CREATE DATABASE %s OWNER %s;

\connect %s

-- %s owns the schema and runs the migrations; %s only uses it.
REVOKE ALL   ON SCHEMA public FROM PUBLIC;
GRANT  ALL   ON SCHEMA public TO %s;
GRANT  USAGE ON SCHEMA public TO %s;

-- `+"`tide migrate`"+` creates the tables and grants %s INSERT and SELECT —
-- and nothing else — on audit_log and release_approvals. That grant is what
-- makes the record append-only: it is enforced by PostgreSQL, not by the
-- absence of an UPDATE statement in Tide.
`,
		ident(owner.Role), pw(owner, "TIDE_DATABASE_MIGRATE_URL"),
		ident(app.Role), pw(app, "TIDE_DATABASE_URL"),
		ident(app.Database), ident(owner.Role),
		ident(app.Database),
		owner.Role, app.Role,
		ident(owner.Role),
		ident(app.Role),
		app.Role)
	return b.String(), nil
}

// quote renders a string literal. A password may contain a quote, and this
// text is pasted into psql: doubling is PostgreSQL's own escape.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ident renders an identifier. Role and database names come from a URL, so
// they are attacker-influenced in exactly the same way a password is.
func ident(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// ErrNeedsBootstrap reports that the database or its roles are missing,
// rather than the server being unreachable or the password being wrong.
var ErrNeedsBootstrap = errors.New("database or roles do not exist")

// NeedsBootstrap classifies a connection failure. PostgreSQL answers with
// 3D000 for a database that does not exist and 28000 for a role that does
// not; a wrong password is 28P01 and is not this.
func NeedsBootstrap(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "3D000" || pgErr.Code == "28000"
}
