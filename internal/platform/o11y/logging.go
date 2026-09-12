package o11y

import (
	"context"
	"io"
	"log/slog"

	"github.com/lmittmann/tint"
)

type ctxKey struct{}

type ContextHandler struct {
	slog.Handler
}

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(ctxKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	if s, ok := ctx.Value(scopeKey{}).(*scope); ok {
		s.addTo(&r)
	}
	return h.Handler.Handle(ctx, r)
}

func (h ContextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return ContextHandler{h.Handler.WithAttrs(as)}
}
func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{h.Handler.WithGroup(name)}
}

func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	next := make([]slog.Attr, 0, len(prev)+len(attrs))
	next = append(next, prev...)
	next = append(next, attrs...)
	return context.WithValue(ctx, ctxKey{}, next)
}

type LoggingConfig struct {
	Level   slog.Level
	Pretty  bool
	Version string
}

func NewLogger(cfg LoggingConfig, w io.Writer) *slog.Logger {
	var h slog.Handler
	if cfg.Pretty {
		h = tint.NewTextHandler(w, &tint.Options{
			Level:     cfg.Level,
			AddSource: cfg.Level <= slog.LevelDebug,
		})
	} else {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:     cfg.Level,
			AddSource: cfg.Level <= slog.LevelDebug,
		})
	}

	return slog.New(ContextHandler{h}).With(
		slog.String("version", cfg.Version),
	)
}
