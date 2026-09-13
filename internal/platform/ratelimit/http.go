package ratelimit

import (
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/danielgtaylor/huma/v2"
)

const message = "Too many requests. Try again later."

func RetryAfter(delay time.Duration) string {
	seconds := delay / time.Second
	if delay%time.Second != 0 {
		seconds++
	}
	return strconv.FormatInt(max(1, int64(seconds)), 10)
}

func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay := l.Allow(IPKey(r.RemoteAddr)); delay > 0 {
			w.Header().Set("Retry-After", RetryAfter(delay))
			apierr.Write(w, r, http.StatusTooManyRequests, message)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Error(delay time.Duration) error {
	return huma.ErrorWithHeaders(huma.Error429TooManyRequests(message), http.Header{
		"Retry-After":   {RetryAfter(delay)},
		"Cache-Control": {"no-store"},
	})
}

func WriteError(api huma.API, ctx huma.Context, delay time.Duration) error {
	ctx.SetHeader("Retry-After", RetryAfter(delay))
	ctx.SetHeader("Cache-Control", "no-store")
	return huma.WriteErr(api, ctx, http.StatusTooManyRequests, message)
}

func Document(api huma.API, op *huma.Operation) {
	if op.Responses[strconv.Itoa(http.StatusTooManyRequests)] != nil {
		return
	}
	minimum := 1.0
	op.Responses[strconv.Itoa(http.StatusTooManyRequests)] = &huma.Response{
		Description: http.StatusText(http.StatusTooManyRequests),
		Headers: map[string]*huma.Header{
			"Retry-After": {Description: "Seconds before the next attempt.", Schema: &huma.Schema{Type: "integer", Minimum: &minimum}},
		},
		Content: map[string]*huma.MediaType{apierr.ContentType: {
			Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[apierr.Problem](), true, "Problem"),
		}},
	}
}
