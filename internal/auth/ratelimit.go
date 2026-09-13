package auth

import (
	"time"

	"github.com/bmardale/stocat/internal/platform/ratelimit"
	"github.com/danielgtaylor/huma/v2"
)

func (s *Service) limitCredentials(api huma.API, limits ...*ratelimit.Limiter) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		key := ratelimit.IPKey(ctx.RemoteAddr())
		for _, limit := range limits {
			if delay := limit.Allow(key); delay > 0 {
				s.writeRateLimitError(api, ctx, delay)
				return
			}
		}
		next(ctx)
	}
}

func (s *Service) writeRateLimitError(api huma.API, ctx huma.Context, delay time.Duration) {
	if err := ratelimit.WriteError(api, ctx, delay); err != nil {
		s.log.ErrorContext(ctx.Context(), "write rate limit error", "error", err)
	}
}
