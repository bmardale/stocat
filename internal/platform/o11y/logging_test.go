package o11y

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewAddsConfigAndContextAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggingConfig{
		Level:   slog.LevelInfo,
		Version: "v1",
	}, &output)

	ctx := With(context.Background(), slog.String("request_id", "req-1"))
	logger.InfoContext(ctx, "started", slog.Int("attempt", 2))

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode log output: %v\noutput: %s", err, output.String())
	}

	for key, want := range map[string]any{
		"msg":        "started",
		"version":    "v1",
		"request_id": "req-1",
		"attempt":    float64(2),
	} {
		if got := record[key]; got != want {
			t.Errorf("log attribute %q = %#v, want %#v", key, got, want)
		}
	}
}

func TestNewPrettyUsesTextAndRespectsLevel(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggingConfig{Level: slog.LevelInfo, Pretty: true}, &output)

	logger.Debug("hidden")
	logger.Info("visible")

	got := output.String()
	if bytes.Contains(output.Bytes(), []byte("hidden")) {
		t.Errorf("debug log was emitted: %q", got)
	}
	if !bytes.Contains(output.Bytes(), []byte("visible")) {
		t.Errorf("text log output = %q, want visible message", got)
	}
	if !bytes.Contains(output.Bytes(), []byte("INF")) {
		t.Errorf("text log output = %q, want tint level marker", got)
	}
	if bytes.Contains(output.Bytes(), []byte(`"msg"`)) {
		t.Errorf("pretty log output = %q, want text not JSON", got)
	}
}
