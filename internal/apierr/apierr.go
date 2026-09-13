// Package apierr gives every failed request the same JSON body.
package apierr

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

// ContentType is the media type of an error body. RFC 9457 defines it.
const ContentType = "application/problem+json"

// Problem is the body of every failed request. It follows RFC 9457.
type Problem struct {
	huma.ErrorModel
	RequestID string `json:"request_id,omitempty" doc:"Identifier of the request. Give this value to support." example:"MJQXE43UNFWWK"`
}

// New builds a problem for a status code. Each error in errs becomes an
// entry in the errors list of the body.
func New(status int, detail string, errs ...error) *Problem {
	problem := &Problem{}
	problem.Status = status
	problem.Title = http.StatusText(status)
	problem.Detail = detail
	for _, err := range errs {
		if err != nil {
			problem.Add(err)
		}
	}
	return problem
}

// Install makes huma answer with a problem body. It changes package
// variables of huma, so call it one time before you build the API.
func Install(cfg *huma.Config) {
	huma.NewError = func(status int, detail string, errs ...error) huma.StatusError {
		return New(status, detail, errs...)
	}
	cfg.Transformers = append(cfg.Transformers, addRequestID)
}

// addRequestID puts the request identifier in the body. huma builds an
// error before it knows the request, so the value goes in at write time.
func addRequestID(ctx huma.Context, _ string, value any) (any, error) {
	if problem, ok := value.(*Problem); ok && problem.RequestID == "" {
		problem.RequestID = o11y.RequestIDFrom(ctx.Context())
	}
	return value, nil
}

// Write sends a problem body for a request that reaches no operation.
func Write(w http.ResponseWriter, r *http.Request, status int, detail string) {
	problem := New(status, detail)
	problem.RequestID = o11y.RequestIDFrom(r.Context())
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem)
}

// Handler answers every request with the same problem.
func Handler(status int, detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Write(w, r, status, detail)
	}
}

// MethodNotAllowed answers with status 405. It also lists the methods that
// the router accepts, because RFC 9110 needs the Allow header on a 405.
func MethodNotAllowed(router *chi.Mux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if allowed := allowedMethods(router, r.URL.Path); len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
		Write(w, r, http.StatusMethodNotAllowed, "This path does not accept the request method.")
	}
}

func allowedMethods(router *chi.Mux, path string) []string {
	var allowed []string
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		if router.Match(chi.NewRouteContext(), method, path) {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

// Recoverer logs a panic in a handler and answers with status 500.
// It replaces the chi recoverer, which writes to standard output.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				value := recover()
				if value == nil {
					return
				}
				if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(value)
				}
				logger.LogAttrs(r.Context(), slog.LevelError, "handler panic",
					slog.Any("panic", value),
					slog.String("stack", string(debug.Stack())),
				)
				Write(w, r, http.StatusInternalServerError, "The server failed to handle the request.")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
