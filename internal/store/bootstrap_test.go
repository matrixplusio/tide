package store

import (
	"strings"
	"testing"

	"tide/internal/store/migration"
)

const (
	appURL   = "postgres://tide_app:app-secret@db:5432/tide?sslmode=require"
	ownerURL = "postgres://tide_owner:owner-secret@db:5432/tide?sslmode=require"
)

func TestBootstrapSQLFillsInWhatWasConfigured(t *testing.T) {
	sql, err := BootstrapSQL(appURL, ownerURL, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CREATE ROLE "tide_owner" LOGIN PASSWORD 'owner-secret';`,
		`CREATE ROLE "tide_app" LOGIN PASSWORD 'app-secret';`,
		`CREATE DATABASE "tide" OWNER "tide_owner";`,
		`GRANT  USAGE ON SCHEMA public TO "tide_app";`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing:\n  %s\ngot:\n%s", want, sql)
		}
	}
}

// The same text goes into a log when a pod cannot start, and a log must
// never carry a secret (CONVENTIONS.md §3).
func TestBootstrapSQLCanWithholdPasswords(t *testing.T) {
	sql, err := BootstrapSQL(appURL, ownerURL, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"app-secret", "owner-secret"} {
		if strings.Contains(sql, secret) {
			t.Fatalf("the password %q reached a form meant for logs:\n%s", secret, sql)
		}
	}
	// Still useful: it names the roles and says where the passwords are.
	if !strings.Contains(sql, "TIDE_DATABASE_MIGRATE_URL") || !strings.Contains(sql, `CREATE ROLE "tide_owner"`) {
		t.Fatalf("the redacted form is not actionable:\n%s", sql)
	}
}

// The output is pasted into psql. A password or a role name is attacker
// influenced in exactly the same way any other input is.
func TestBootstrapSQLEscapes(t *testing.T) {
	tests := []struct {
		name, password, want string
	}{
		{"a quote", `pa'ss`, `PASSWORD 'pa''ss'`},
		{"an injection attempt", `x'; DROP DATABASE tide; --`, `PASSWORD 'x''; DROP DATABASE tide; --'`},
		{"a backslash", `pa\ss`, `PASSWORD 'pa\ss'`},
		{"non-latin", `密码-很长-够用了吗`, `PASSWORD '密码-很长-够用了吗'`},
	}
	for _, tt := range tests {
		sql, err := BootstrapSQL("postgres://tide_app:"+urlEscape(tt.password)+"@db:5432/tide", ownerURL, true)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !strings.Contains(sql, tt.want) {
			t.Errorf("%s: want %s in\n%s", tt.name, tt.want, sql)
		}
	}
}

func TestBootstrapSQLRefusesAConfigurationThatCannotWork(t *testing.T) {
	tests := []struct {
		name, app, owner, wantMsg string
	}{
		{"different databases", "postgres://tide_app:p@db/tide", "postgres://tide_owner:p@db/other", "different databases"},
		{
			// The second role is the whole mechanism; one role means the
			// runtime account owns audit_log and can rewrite it.
			"one role for both", "postgres://tide_app:p@db/tide", "postgres://tide_app:p@db/tide", "separate owner role",
		},
		{"runtime role is not the one the grants name", "postgres://someone:p@db/tide", ownerURL, "must connect as"},
		{"no app url", "", ownerURL, "TIDE_DATABASE_URL is not set"},
		{"no owner url", appURL, "", "TIDE_DATABASE_MIGRATE_URL is not set"},
		{"no database in the url", "postgres://tide_app:p@db", ownerURL, "no database name"},
	}
	for _, tt := range tests {
		_, err := BootstrapSQL(tt.app, tt.owner, true)
		if err == nil {
			t.Errorf("%s: accepted", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantMsg) {
			t.Errorf("%s: %q does not explain %q", tt.name, err, tt.wantMsg)
		}
	}
}

// The generated SQL and the migrations have to agree about which role gets
// the append-only grant; nothing would fail loudly if they drifted.
func TestBootstrapSQLNamesTheRoleTheMigrationsGrant(t *testing.T) {
	sql, err := BootstrapSQL(appURL, ownerURL, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `"`+migration.AppRole+`"`) {
		t.Fatalf("the SQL does not create %q, which is what the migrations grant to", migration.AppRole)
	}
}

func urlEscape(s string) string {
	r := strings.NewReplacer("'", "%27", "\\", "%5C", " ", "%20", "@", "%40", ";", "%3B")
	return r.Replace(s)
}
