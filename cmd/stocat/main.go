package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/bmardale/stocat/internal/platform/config"
	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/bmardale/stocat/internal/platform/splash"
	"github.com/bmardale/stocat/internal/platform/version"
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

	return nil
}
