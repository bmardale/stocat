package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/platform/crypt"
	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/bmardale/stocat/internal/platform/ratelimit"
	"github.com/bmardale/stocat/internal/platform/version"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type probeOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type Config struct {
	Addr          string
	SecureCookies bool
	Logger        *slog.Logger
	Encrypter     *crypt.Encrypter
	// RateLimitClock defaults to time.Now. It controls all request limiters.
	RateLimitClock func() time.Time
}

type Server struct {
	api        huma.API
	httpServer *http.Server
	stopping   atomic.Bool
	log        *slog.Logger
}

func New(cfg Config, pool *pgxpool.Pool) (*Server, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	limiter, err := ratelimit.New(ratelimit.Policy{Interval: time.Second / 5, Burst: 60}, cfg.RateLimitClock)
	if err != nil {
		return nil, fmt.Errorf("create request limiter: %w", err)
	}

	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(apierr.Handler(http.StatusForbidden, "The request comes from another origin."))

	router := chi.NewRouter()
	router.Use(o11y.RequestID)
	router.Use(o11y.AccessLog(log))
	router.Use(apierr.Recoverer(log))
	router.Use(limitRequests(limiter))
	router.Use(protection.Handler)
	router.NotFound(apierr.Handler(http.StatusNotFound, "This path does not exist."))
	router.MethodNotAllowed(apierr.MethodNotAllowed(router))

	humaCfg := huma.DefaultConfig("stocat", version.API)
	humaCfg.DocsRenderer = huma.DocsRendererScalar
	humaCfg.CreateHooks = nil // omit $schema and Link on responses
	apierr.Install(&humaCfg)
	api := humachi.New(router, humaCfg)
	authService, err := auth.New(pool, auth.Config{SecureCookies: cfg.SecureCookies, Logger: log, RateLimitClock: cfg.RateLimitClock})
	if err != nil {
		return nil, fmt.Errorf("create authentication service: %w", err)
	}
	authService.Register(api)
	storage.New(pool, storage.Config{Encrypter: cfg.Encrypter, Logger: log}).Register(authService.Admin(api, "/admin"))

	s := &Server{api: api, log: log, httpServer: &http.Server{
		Addr: cfg.Addr, Handler: router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}}

	huma.Register(api, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/healthz",
		Summary: "Check service health",
	}, func(context.Context, *struct{}) (*probeOutput, error) {
		return probeOK(), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ready", Method: http.MethodGet, Path: "/readyz",
		Summary: "Check service readiness", Errors: []int{http.StatusServiceUnavailable},
	}, func(ctx context.Context, _ *struct{}) (*probeOutput, error) {
		if s.stopping.Load() {
			return nil, huma.Error503ServiceUnavailable("Service is stopping")
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return nil, huma.Error503ServiceUnavailable("Database is unavailable")
		}
		return probeOK(), nil
	})

	return s, nil
}

// OpenAPI returns the OpenAPI document of the API in YAML format.
func (s *Server) OpenAPI() ([]byte, error) {
	spec, err := s.api.OpenAPI().YAML()
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAPI: %w", err)
	}
	return spec, nil
}

func (s *Server) logger() *slog.Logger {
	if s.log == nil {
		return slog.Default()
	}
	return s.log
}

func probeOK() *probeOutput {
	output := &probeOutput{}
	output.Body.Status = "ok"
	return output
}

func (s *Server) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.logger().Info("HTTP server started", "addr", listener.Addr().String())
	return s.serve(ctx, listener, shutdownTimeout)
}

func (s *Server) serve(ctx context.Context, listener net.Listener, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.httpServer.Serve(listener) }()
	select {
	case err := <-serveErr:
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}
	s.stopping.Store(true)
	s.logger().Info("HTTP server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		closeErr := s.httpServer.Close()
		return fmt.Errorf("shutdown HTTP: %w", errors.Join(err, closeErr))
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}
