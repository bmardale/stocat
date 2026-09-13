package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/bmardale/stocat/internal/platform/crypt"
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

	// WebAuthnRPID must equal the host of each origin or a registrable domain suffix of that host.
	// A change to this value invalidates all registered passkeys.
	WebAuthnRPID      string   `env:"WEBAUTHN_RP_ID" envDefault:"localhost"`
	WebAuthnRPName    string   `env:"WEBAUTHN_RP_NAME" envDefault:"Stocat"`
	WebAuthnRPOrigins []string `env:"WEBAUTHN_RP_ORIGINS" envSeparator:"," envDefault:"http://localhost:5173"`

	// AppKey encrypts secrets in the database. Generate one with "make key-generate".
	AppKey crypt.Key `env:"APP_KEY,required,notEmpty"`
	// AppPreviousKeys decrypt data that an earlier APP_KEY encrypted.
	AppPreviousKeys []crypt.Key `env:"APP_PREVIOUS_KEYS" envSeparator:","`

	LogLevel              slog.Level    `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout       time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	UploadStagingDir      string        `env:"UPLOAD_STAGING_DIR" envDefault:"/var/lib/stocat/uploads"`
	MaxUploadSize         int64         `env:"MAX_UPLOAD_SIZE" envDefault:"10737418240"`
	UploadStagingCapacity int64         `env:"UPLOAD_STAGING_CAPACITY" envDefault:"21474836480"`
	UploadSessionLifetime time.Duration `env:"UPLOAD_SESSION_LIFETIME" envDefault:"24h"`

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
	if c.MaxUploadSize <= 0 || c.UploadStagingCapacity <= 0 || c.UploadSessionLifetime <= 0 {
		return Config{}, fmt.Errorf("upload limits and lifetime must be positive")
	}

	return c, nil
}
