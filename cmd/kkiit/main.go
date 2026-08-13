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
	server := &http.Server{Addr: ":8080", Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	go func() {
		logger.Info("Kkiit started", "address", server.Addr, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
