package config

import (
	"log/slog"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ADDR", ":9090")
	t.Setenv("DATABASE_URL", "postgres://localhost/app")
	t.Setenv("DATABASE_MAX_CONNS", "10")

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}

	if got.Env != EnvProd || got.LogLevel != slog.LevelDebug || got.Addr != ":9090" || got.Postgres.URL != "postgres://localhost/app" || got.Postgres.MaxConns != 10 {
		t.Errorf("Config = %#v, want prod, :9090, debug", got)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "zero shutdown timeout", values: map[string]string{"SHUTDOWN_TIMEOUT": "0s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/app")
			for name, value := range test.values {
				t.Setenv(name, value)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil, want an error")
			}
		})
	}
}
