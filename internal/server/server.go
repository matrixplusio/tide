// Package server assembles configuration, storage, domain services, the
// executor and the gin engine, and runs the HTTP server.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tide/internal/access"
	"tide/internal/auth"
	"tide/internal/cache"
	"tide/internal/catalog"
	"tide/internal/ci"
	"tide/internal/config"
	"tide/internal/crypto"
	"tide/internal/executor"
	"tide/internal/metrics"
	"tide/internal/notify"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	v1 "tide/internal/server/api/v1"
	"tide/internal/server/middleware"
	"tide/internal/settings"
	"tide/internal/setup"
	"tide/internal/store"
	"tide/internal/store/pg"
	"tide/internal/web"
)

// Engine builds the gin engine. web serves the SPA (embedded build or the
// Vite dev proxy) for every non-API path.
func Engine(cfg *config.Config, deps v1.Deps, webHandler http.Handler) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	r.Use(
		middleware.RequestID(),
		middleware.Recover(),
		middleware.Locale(),
		middleware.AccessLog(),
		middleware.SecurityHeaders(cfg.Server.DevWebProxy != ""),
		middleware.CSRF(),
	)
	if cfg.Server.Metrics {
		// No envelope, no session: a scrape target, optionally bearer-gated.
		r.GET("/metrics", gin.WrapH(metrics.Handler(cfg.Server.MetricsToken)))
	}
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := deps.PG.Ping(ctx); err != nil {
			c.String(http.StatusServiceUnavailable, "database unavailable")
			return
		}
		c.String(http.StatusOK, "ok")
	})
	v1.Register(r, deps)
	// Anything the API did not claim is a page: the SPA owns its own routing,
	// so /services/foo is a real page and must answer 200. gin sets 404 on
	// the context before it reaches NoRoute, and writing a body afterwards
	// does not undo that — the status has to be reset explicitly, or every
	// page load answers "404" with the right HTML underneath. Browsers do not
	// mind; health checks, crawlers and uptime monitors do.
	notFound := func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			respond.Fail(c, errcode.NewKey(errcode.NotFound, "g.noSuchEndpoint"))
			return
		}
		c.Status(http.StatusOK)
		webHandler.ServeHTTP(c.Writer, c.Request)
	}
	r.NoRoute(notFound)
	r.NoMethod(notFound)
	return r, nil
}

// Run serves until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config) error {
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	box, err := crypto.New(cfg.Secrets.Key)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer func() { _ = sqlDB.Close() }()
	}
	pgs := pg.New(db)
	set := &settings.Store{PG: pgs, Box: box}
	accessSvc := &access.Service{PG: pgs, Settings: set}
	authSvc := &auth.Service{PG: pgs, Settings: set, Box: box, Access: accessSvc}
	setupSvc := &setup.Service{PG: pgs, Settings: set, Auth: authSvc}
	if err := setupSvc.Init(ctx); err != nil {
		return fmt.Errorf("setup init: %w", err)
	}
	shared, err := sharedCache(ctx, cfg.Redis.URL)
	if err != nil {
		return err
	}
	if closer, ok := shared.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	hub := &catalog.Hub{Settings: set, PG: pgs, Shared: shared}

	webHandler, err := web.Handler(cfg.Server.DevWebProxy)
	if err != nil {
		return err
	}
	notifier := &notify.Notifier{Settings: set}
	ciSvc := &ci.Service{PG: pgs, Settings: set, Hub: hub, Notifier: notifier}
	// Engine takes no context: it assembles handlers, and each one works from
	// its own request's context.
	engine, err := Engine(cfg, v1.Deps{PG: pgs, Settings: set, Auth: authSvc, Access: accessSvc, Setup: setupSvc, //nolint:contextcheck // see above
		Hub: hub, Notifier: notifier, CI: ciSvc}, webHandler)
	if err != nil {
		return err
	}

	exec := &executor.Engine{
		PG:       pgs,
		Settings: set,
		Executors: map[string]executor.ItemExecutor{
			release.KindImage:   executor.Image{Hub: hub},
			release.KindRestart: executor.Restart{Hub: hub},
			release.KindSync:    executor.Sync{Hub: hub},
		},
		Notifier: notifier,
		OnChange: hub.Reset,
	}
	go exec.Run(ctx)
	go ciSvc.Run(ctx)
	go purgeSessions(ctx, authSvc)

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	if cfg.Server.DevWebProxy != "" {
		srv.WriteTimeout = 0 // Vite HMR keeps a websocket open through the proxy
	}
	go func() { //nolint:gosec // shutdown must outlive the cancelled serve context
		<-ctx.Done()
		// A fresh context for the same reason: this runs because ctx is done.
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx) //nolint:contextcheck // see above
	}()
	zap.L().Info("listening", zap.String("addr", cfg.Server.Addr), zap.Bool("dev", cfg.Server.DevWebProxy != ""))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// sharedCache returns the tier the replicas have in common. A configured URL
// that does not work fails startup: falling back quietly would leave an
// operator believing replicas share a cache when each has its own.
func sharedCache(ctx context.Context, url string) (cache.Cache, error) {
	if url == "" {
		zap.L().Info("no shared cache configured, each replica keeps its own")
		return &cache.Memory{}, nil
	}
	r, err := cache.NewRedis(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("shared cache: %w", err)
	}
	zap.L().Info("shared cache connected")
	return r, nil
}

func purgeSessions(ctx context.Context, a *auth.Service) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.PurgeExpired(ctx); err != nil {
				zap.L().Warn("purge expired sessions failed", zap.Error(err))
			}
			if err := a.PG.Accounts.PurgeExpiredCaptchas(ctx); err != nil {
				zap.L().Warn("purge expired captchas failed", zap.Error(err))
			}
		}
	}
}
