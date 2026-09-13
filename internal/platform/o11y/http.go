package o11y

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// RequestIDHeader carries the request identifier in requests and responses.
const RequestIDHeader = "X-Request-Id"

const (
	maxRequestIDLen = 64
	maxRouteLen     = 256
)

type requestIDKey struct{}

// RequestID puts a request identifier in the context and the response header.
// The middleware keeps the client identifier if the client sends a valid one.
// It also starts the log scope for the request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := cleanRequestID(r.Header.Get(RequestIDHeader))
		if requestID == "" {
			requestID = id.New(id.Request)
		}
		w.Header().Set(RequestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = With(NewScope(ctx), slog.String("request_id", requestID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request identifier in ctx, or an empty string.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// cleanRequestID rejects a value that is not safe to log. The client
// controls this value, and it goes to the log file.
func cleanRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLen {
		return ""
	}
	for i := range len(id) {
		if id[i] <= ' ' || id[i] > '~' {
			return ""
		}
	}
	return id
}

// AccessLog writes one record for each request that the server completes.
// Probe requests that succeed go to the debug level to limit noise.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := recorder.Status()
				if status == 0 {
					status = http.StatusOK
				}
				ctx := r.Context()
				logger.LogAttrs(ctx, accessLevel(r.URL.Path, status), "request handled",
					slog.String("method", r.Method),
					slog.String("route", route(r)),
					slog.Int("status", status),
					slog.Int("bytes", recorder.BytesWritten()),
					slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
					slog.String("remote_ip", remoteIP(r)),
					slog.String("user_agent", r.UserAgent()),
				)
			}()
			next.ServeHTTP(recorder, r)
		})
	}
}

func accessLevel(path string, status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case path == "/healthz" || path == "/readyz":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// route returns the route pattern, or the request path when no route matches.
// A 404 has no pattern, and the path is the value that helps.
func route(r *http.Request) string {
	pattern := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		pattern = rc.RoutePattern()
	}
	if pattern == "" {
		pattern = r.URL.Path
	}
	if len(pattern) > maxRouteLen {
		return pattern[:maxRouteLen]
	}
	return pattern
}

// remoteIP ignores forwarding headers. A client can set them, because no
// trusted proxy removes them yet.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
