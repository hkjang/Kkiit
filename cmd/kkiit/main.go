package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hkjang/Kkiit/internal/config"
	"github.com/hkjang/Kkiit/internal/cryptox"
	"github.com/hkjang/Kkiit/internal/database"
	"github.com/hkjang/Kkiit/internal/httpapi"
	"github.com/hkjang/Kkiit/internal/worker"
)

var (
	version = "0.0.0-dev"
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureBootstrapAdmin(ctx, pool, cfg.BootstrapAdmin, cfg.BootstrapAdminPassword); err != nil {
		logger.Error("bootstrap admin failed", "error", err)
		os.Exit(1)
	}
	box, err := cryptox.New(cfg.EncryptionKey)
	if err != nil {
		logger.Error("encryption initialization failed", "error", err)
		os.Exit(1)
	}
	api := &httpapi.Server{DB: pool, Box: box, Version: version, Commit: commit, BuiltAt: builtAt, Logger: logger}
	dispatcher := &worker.Worker{DB: pool, Box: box, Logger: logger, Publish: api.PublishUser}
	api.Rescan = dispatcher.ScanNow
	dispatcher.Maintenance = api.RunOrderMaintenance
	// The dispatcher claims events inside a transaction. Exiting without waiting
	// for it leaves that work to the stuck-event sweeper on the next start,
	// which recovers it but only after a delay nobody asked for.
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		dispatcher.Run(ctx)
	}()
	server := &http.Server{Addr: ":8080", Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	go func() {
		logger.Info("Kkiit started", "address", server.Addr, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	// Readiness fails first and the socket stays open for a moment, so whatever
	// routes traffic here can take this instance out before it disappears. Going
	// straight to Shutdown means every request already on its way to this
	// process fails, which during a rolling deploy is every request.
	api.BeginDrain()
	logger.Info("draining before shutdown", "seconds", cfg.DrainSeconds)
	if cfg.DrainSeconds > 0 {
		time.Sleep(time.Duration(cfg.DrainSeconds) * time.Second)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	select {
	case <-dispatcherDone:
	case <-time.After(15 * time.Second):
		logger.Warn("event dispatcher did not stop in time")
	}
	logger.Info("Kkiit stopped")
}
