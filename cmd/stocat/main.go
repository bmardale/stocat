package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/config"
	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/bmardale/stocat/internal/platform/splash"
	"github.com/bmardale/stocat/internal/platform/version"
	"github.com/bmardale/stocat/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("stocat failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := o11y.NewLogger(o11y.LoggingConfig{
		Level:   cfg.LogLevel,
		Pretty:  cfg.Env != config.EnvProd,
		Version: version.Build(),
	}, os.Stderr)

	slog.SetDefault(log)

	if cfg.Env != config.EnvProd {
		if err := splash.Print(os.Stdout, version.Build()); err != nil {
			slog.Warn("print splash", "error", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := db.Open(startupCtx, cfg.Postgres.URL, cfg.Postgres.MaxConns)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	log.Info("config loaded",
		"env", cfg.Env,
		"addr", cfg.Addr,
		"log_level", cfg.LogLevel,
	)

	srv, err := server.New(server.Config{
		Addr:          cfg.Addr,
		SecureCookies: cfg.SessionCookieSecure,
		Logger:        log,
	}, pool)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	return srv.Run(ctx, cfg.ShutdownTimeout)
}
