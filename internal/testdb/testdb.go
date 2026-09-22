// Package testdb gives integration tests a freshly migrated database. Tests
// are skipped (with a reason) unless TIDE_TEST_OWNER_URL and TIDE_TEST_APP_URL
// are set — see `make test-db`.
package testdb

import (
	"context"
	"os"
	"sync"
	"testing"

	"gorm.io/gorm"

	"tide/internal/store"
	"tide/internal/store/migration"
	"tide/internal/store/pg"
)

var serial sync.Mutex

// Setup resets the schema as owner and returns the app-account store and the
// owner connection (for rewriting test data such as timestamps).
func Setup(t *testing.T) (*pg.Store, *gorm.DB) {
	t.Helper()
	ownerURL, appURL := os.Getenv("TIDE_TEST_OWNER_URL"), os.Getenv("TIDE_TEST_APP_URL")
	if ownerURL == "" || appURL == "" {
		t.Skip("database tests need TIDE_TEST_OWNER_URL and TIDE_TEST_APP_URL (run make test-db)")
	}
	serial.Lock()
	t.Cleanup(serial.Unlock)
	ctx := context.Background()
	owner, err := store.Open(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(owner) })
	// Drop whatever is there rather than a hand-written list: a list has to
	// be edited with every migration, and forgetting leaves a stale table
	// that makes the next migration fail with "already exists".
	if err := owner.Exec(`DO $$
		DECLARE t text;
		BEGIN
			FOR t IN SELECT tablename FROM pg_tables WHERE schemaname = current_schema() LOOP
				EXECUTE format('DROP TABLE IF EXISTS %I CASCADE', t);
			END LOOP;
		END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	app, err := store.Open(ctx, appURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(app) })
	return pg.New(app), owner
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
