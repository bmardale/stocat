package config

import (
	"log/slog"
	"testing"

	"github.com/bmardale/stocat/internal/platform/crypt"
)

func TestLoadConfig(t *testing.T) {
	key, previous := crypt.GenerateKey(), crypt.GenerateKey()
	t.Setenv("APP_ENV", "prod")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ADDR", ":9090")
	t.Setenv("DATABASE_URL", "postgres://localhost/app")
	t.Setenv("DATABASE_MAX_CONNS", "10")
	t.Setenv("APP_KEY", key)
	t.Setenv("APP_PREVIOUS_KEYS", previous+","+crypt.GenerateKey())

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got.Env != EnvProd || got.LogLevel != slog.LevelDebug || got.Addr != ":9090" || got.Postgres.URL != "postgres://localhost/app" || got.Postgres.MaxConns != 10 {
		t.Errorf("Config = %#v, want prod, :9090, debug", got)
	}
	if len(got.AppKey) != crypt.KeySize || len(got.AppPreviousKeys) != 2 {
		t.Errorf("AppKey has %d bytes and %d previous keys, want %d bytes and 2 keys", len(got.AppKey), len(got.AppPreviousKeys), crypt.KeySize)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "zero shutdown timeout", values: map[string]string{"SHUTDOWN_TIMEOUT": "0s"}},
		{name: "missing app key", values: map[string]string{"APP_KEY": ""}},
		{name: "app key without prefix", values: map[string]string{"APP_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}},
		{name: "short app key", values: map[string]string{"APP_KEY": "base64:AAAAAAAAAAAAAAAAAAAAAA=="}},
		{name: "invalid previous key", values: map[string]string{"APP_PREVIOUS_KEYS": "base64:invalid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/app")
			t.Setenv("APP_KEY", crypt.GenerateKey())
			for name, value := range test.values {
				t.Setenv(name, value)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil, want an error")
			}
		})
	}
}
