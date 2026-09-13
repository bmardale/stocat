package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestServer(t *testing.T, cfg Config, pool *pgxpool.Pool) *Server {
	t.Helper()
	if cfg.RateLimitClock == nil {
		now := time.Now()
		cfg.RateLimitClock = func() time.Time { return now }
	}
	s, err := New(cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProbes(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		stopping   bool
		status     int
	}{
		{"health", "/healthz", false, http.StatusOK},
		{"health during shutdown", "/healthz", true, http.StatusOK},
		{"readiness during shutdown", "/readyz", true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, Config{SecureCookies: true}, nil)
			s.stopping.Store(tc.stopping)
			api := humatest.Wrap(t, s.api)
			response := api.GetCtx(t.Context(), tc.path)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}

func TestGlobalRateLimit(t *testing.T) {
	now := time.Now()
	s := newTestServer(t, Config{Logger: slog.New(slog.DiscardHandler), RateLimitClock: func() time.Time { return now }}, nil)
	s.stopping.Store(true)
	request := func(path, remote string, attempt int) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", attempt))
		req.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", attempt))
		req.Header.Set("Forwarded", fmt.Sprintf("for=203.0.113.%d", attempt))
		req.Header.Set("Cookie", fmt.Sprintf("%s=%064d", "stocat_session", attempt))
		response := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(response, req)
		return response
	}
	for i := range 60 {
		if response := request(fmt.Sprintf("/missing/%d", i), "192.0.2.1:1234", i); response.Code != http.StatusNotFound {
			t.Fatalf("request %d: %d %s", i, response.Code, response.Body)
		}
	}
	response := request("/auth/me", "[::ffff:192.0.2.1]:5678", 61)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing rate limit response: %d %s", response.Code, response.Body)
	}
	var problem apierr.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != http.StatusTooManyRequests || problem.RequestID == "" || problem.RequestID != response.Header().Get(o11y.RequestIDHeader) {
		t.Fatalf("invalid problem: %+v", problem)
	}
	for path, status := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		if response := request(path, "192.0.2.1:1234", 62); response.Code != status {
			t.Fatalf("probe %s was limited: %d", path, response.Code)
		}
	}
	if response := request("/missing", "192.0.2.2:1234", 63); response.Code != http.StatusNotFound {
		t.Fatalf("another IP was limited: %d", response.Code)
	}
	now = now.Add(200 * time.Millisecond)
	if response := request("/missing", "192.0.2.1:1234", 64); response.Code != http.StatusNotFound {
		t.Fatalf("refilled token was rejected: %d", response.Code)
	}
	if response := request("/missing", "192.0.2.1:1234", 65); response.Code != http.StatusTooManyRequests {
		t.Fatalf("refill admitted extra requests: %d", response.Code)
	}
}

func TestAuthCrossOriginProtection(t *testing.T) {
	s := newTestServer(t, Config{SecureCookies: true, Logger: slog.New(slog.DiscardHandler)}, nil)
	for _, path := range []string{"/auth/register", "/auth/login", "/auth/logout"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://attacker.example")
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			response := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || response.Header().Get("Set-Cookie") != "" {
				t.Fatalf("cross-origin request was not rejected: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestShutdown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		release bool
	}{
		{"drain", time.Second, true},
		{"deadline", 20 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			s := &Server{httpServer: &http.Server{
				ReadHeaderTimeout: time.Second,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(entered)
					select {
					case <-release:
						w.WriteHeader(http.StatusNoContent)
					case <-r.Context().Done():
					}
				}),
			}}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.serve(ctx, listener, tc.timeout) }()
			responseDone := make(chan error, 1)
			go func() {
				client := &http.Client{Timeout: 3 * time.Second}
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
				if err != nil {
					responseDone <- err
					return
				}
				resp, err := client.Do(req)
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					err = errors.Join(err, resp.Body.Close())
					if resp.StatusCode != http.StatusNoContent {
						err = errors.New("unexpected response status")
					}
				}
				responseDone <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			if tc.release {
				select {
				case err := <-done:
					t.Fatalf("shutdown returned before request finished: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				release <- struct{}{}
			}
			select {
			case err := <-done:
				if tc.release && err != nil {
					t.Fatal(err)
				}
				if !tc.release && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("want deadline error, got %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not finish")
			}
			if err := <-responseDone; tc.release && err != nil {
				t.Fatal(err)
			}
			if !s.stopping.Load() {
				t.Fatal("server did not stop readiness")
			}
		})
	}
}

func TestReadinessUnavailable(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), "postgres://postgres:postgres@"+addr+"/stocat?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := newTestServer(t, Config{SecureCookies: true}, pool)
	api := humatest.Wrap(t, s.api)
	response := api.GetCtx(t.Context(), "/readyz")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestRouterAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(t, Config{
		SecureCookies: true,
		Logger:        o11y.NewLogger(o11y.LoggingConfig{Level: slog.LevelDebug}, &buf),
	}, nil)

	recorder := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	id := recorder.Header().Get(o11y.RequestIDHeader)
	if id == "" {
		t.Fatal("no request identifier in the response header")
	}

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode log output: %v\noutput: %s", err, buf.String())
	}
	for key, want := range map[string]any{
		"msg":        "request handled",
		"route":      "/healthz",
		"status":     float64(http.StatusOK),
		"request_id": id,
	} {
		if got := record[key]; got != want {
			t.Errorf("log attribute %q = %#v, want %#v", key, got, want)
		}
	}
}

func TestErrorEnvelope(t *testing.T) {
	s := newTestServer(t, Config{SecureCookies: true, Logger: slog.New(slog.DiscardHandler)}, nil)

	for _, tc := range []struct {
		name, method, path, origin string
		status                     int
	}{
		{name: "unknown path", method: http.MethodGet, path: "/missing", status: http.StatusNotFound},
		{name: "wrong method", method: http.MethodDelete, path: "/healthz", status: http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader("{"))
			request.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			recorder := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.status, recorder.Body)
			}
			if got := recorder.Header().Get("Content-Type"); got != apierr.ContentType {
				t.Errorf("Content-Type = %q, want %q", got, apierr.ContentType)
			}
			var problem apierr.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode body: %v\nbody: %s", err, recorder.Body)
			}
			if problem.Status != tc.status || problem.Title != http.StatusText(tc.status) || problem.Detail == "" {
				t.Errorf("body = %+v", problem)
			}
			if problem.RequestID != recorder.Header().Get(o11y.RequestIDHeader) {
				t.Errorf("body request_id = %q, want the header value %q",
					problem.RequestID, recorder.Header().Get(o11y.RequestIDHeader))
			}
		})
	}
}
