package server

import (
	"net/http"

	"github.com/bmardale/stocat/internal/platform/ratelimit"
)

func limitRequests(limiter *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		limited := limiter.Middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}
}
