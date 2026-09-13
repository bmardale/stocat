package auth

import (
	"time"

	"github.com/bmardale/stocat/internal/platform/ratelimit"
	"github.com/danielgtaylor/huma/v2"
)

func (s *Service) limitCredentials(api huma.API, limits ...*ratelimit.Limiter) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		key := ratelimit.IPKey(ctx.RemoteAddr())
		checks := make([]ratelimit.Check, len(limits))
		for i, limit := range limits {
			checks[i] = ratelimit.Check{Limiter: limit, Key: key}
		}
		if delay := ratelimit.AllowAll(checks...); delay > 0 {
			s.writeRateLimitError(api, ctx, delay)
			return
		}
		next(ctx)
	}
}

func (s *Service) writeRateLimitError(api huma.API, ctx huma.Context, delay time.Duration) {
	if err := ratelimit.WriteError(api, ctx, delay); err != nil {
		s.log.ErrorContext(ctx.Context(), "write rate limit error", "error", err)
	}
}
