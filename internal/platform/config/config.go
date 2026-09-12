package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Env string

const (
	EnvDev  Env = "dev"
	EnvProd Env = "prod"
)

type Config struct {
	Env  Env    `env:"APP_ENV" envDefault:"dev"`
	Addr string `env:"ADDR" envDefault:":8080"`

	SessionCookieSecure bool `env:"SESSION_COOKIE_SECURE" envDefault:"true"`

	LogLevel        slog.Level    `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`

	Postgres struct {
		URL      string `env:"DATABASE_URL,required"`
		MaxConns int32  `env:"DATABASE_MAX_CONNS" envDefault:"20"`
	}
}

func LoadConfig() (Config, error) {
	c, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, err
	}
	if c.ShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be positive")
	}

	return c, nil
}
