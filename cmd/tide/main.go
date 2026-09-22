package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"tide/internal/config"
	"tide/internal/logging"
	"tide/internal/metrics"
	"tide/internal/server"
	"tide/internal/store"
	"tide/internal/store/migration"
	"tide/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("tide", flag.ContinueOnError)
	configPath := fs.String("config", os.Getenv("TIDE_CONFIG"), "path to tide.yaml (optional; TIDE_* env vars override)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	cmd := "serve"
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}

	// Answered before the configuration is read: "what is this binary?" must
	// not need a database URL to answer.
	if cmd == "version" {
		fmt.Print(version.Get())
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	log := logging.New(cfg.Log.Level)
	defer func() { _ = log.Sync() }()
	// One line at startup, so a pod's logs say which build they came from.
	info := version.Get()
	log.Info("tide", zap.String("version", info.Version), zap.String("commit", info.Commit),
		zap.String("built", info.Date), zap.String("go", info.Go), zap.String("platform", info.Platform))
	metrics.BuildInfo(info.Version, info.Commit, info.Go)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		err = server.Run(ctx, cfg)
	case "migrate":
		err = migrate(ctx, cfg)
	case "sql":
		// Printed, never executed: creating roles and databases needs a
		// superuser, and Tide holding one would put the whole PostgreSQL
		// instance behind a single compromised pod.
		var out string
		if out, err = store.BootstrapSQL(cfg.Database.URL, cfg.Database.MigrateURL, true); err == nil {
			fmt.Print(out)
		}
	default:
		err = fmt.Errorf("unknown command %q (serve | migrate | sql | version)", cmd)
	}
	if err != nil {
		log.Error("fatal", zap.String("command", cmd), zap.Error(err))
		return 1
	}
	return 0
}

// migrate runs as the schema owner. The database may start after Tide on a
// fresh cluster, so connecting is retried for a minute.
func migrate(ctx context.Context, cfg *config.Config) error {
	if err := cfg.ValidateMigrate(); err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		db, err := store.Open(ctx, cfg.Database.MigrateURL)
		if err == nil {
			defer func() {
				if sqlDB, err := db.DB(); err == nil {
					_ = sqlDB.Close()
				}
			}()
			if err := migration.Migrate(ctx, db); err != nil {
				return err
			}
			zap.L().Info("migrations applied")
			return nil
		}
		lastErr = err
		if store.NeedsBootstrap(err) {
			// Not "not ready yet" but "was never created". Say what to run,
			// with the passwords withheld: this goes to a log.
			if sql, sqlErr := store.BootstrapSQL(cfg.Database.URL, cfg.Database.MigrateURL, false); sqlErr == nil {
				zap.L().Error("the database or its roles do not exist; create them once as a superuser, then this pod starts on its own",
					zap.String("sql", sql), zap.String("filled_in_by", "tide sql"), zap.Error(err))
				return err
			}
		}
		zap.L().Warn("database not ready, retrying", zap.Int("attempt", attempt), zap.Error(err))
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case <-time.After(2 * time.Second):
		}
	}
	return lastErr
}
