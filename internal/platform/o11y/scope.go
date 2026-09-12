package o11y

import (
	"context"
	"log/slog"
	"sync"
)

type scopeKey struct{}

// scope is a pointer, so middleware that runs before the handler still sees
// the attributes that the handler adds. A context value cannot go back up.
type scope struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

func (s *scope) add(attrs []slog.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attrs = append(s.attrs, attrs...)
}

func (s *scope) addTo(r *slog.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.AddAttrs(s.attrs...)
}

// NewScope returns a context that collects log attributes for one request.
// Attributes that AddAttrs puts in the scope go to every later log record.
func NewScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeKey{}, &scope{})
}

// AddAttrs adds attributes to the request scope in ctx.
// The call does nothing if ctx has no scope.
func AddAttrs(ctx context.Context, attrs ...slog.Attr) {
	if s, ok := ctx.Value(scopeKey{}).(*scope); ok {
		s.add(attrs)
	}
}
