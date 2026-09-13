package o11y

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// records decodes the JSON log records in the buffer.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(buf.String()) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		out = append(out, record)
	}
	return out
}

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return NewLogger(LoggingConfig{Level: slog.LevelDebug}, buf)
}

func TestRequestIDHeader(t *testing.T) {
	for _, tc := range []struct {
		name, sent string
		keep       bool
	}{
		{name: "generated when absent"},
		{name: "kept when valid", sent: "trace-abc-123", keep: true},
		{name: "kept when maximum length", sent: strings.Repeat("a", maxRequestIDLen), keep: true},
		{name: "replaced when too long", sent: strings.Repeat("a", maxRequestIDLen+1)},
		{name: "replaced when it holds a space", sent: "bad id"},
		{name: "replaced when it holds a newline", sent: "bad\nid"},
		{name: "replaced when it holds a control character", sent: "bad\x00id"},
		{name: "replaced when it holds non-ASCII", sent: "idé"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = RequestIDFrom(r.Context())
			}))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.sent != "" {
				request.Header.Set(RequestIDHeader, tc.sent)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if got == "" {
				t.Fatal("no request identifier in the context")
			}
			if header := recorder.Header().Get(RequestIDHeader); header != got {
				t.Errorf("response header = %q, want %q", header, got)
			}
			if tc.keep && got != tc.sent {
				t.Errorf("request identifier = %q, want the sent value %q", got, tc.sent)
			}
			if !tc.keep && got == tc.sent {
				t.Errorf("request identifier %q was not replaced", got)
			}
		})
	}
}

func TestRequestIDGoesToLogRecords(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "in handler")
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "req-1")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	all := records(t, &buf)
	if len(all) != 1 {
		t.Fatalf("log records = %d, want 1", len(all))
	}
	if all[0]["request_id"] != "req-1" {
		t.Errorf("request_id = %#v, want %q", all[0]["request_id"], "req-1")
	}
}

func TestAccessLog(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		status           int
		level            string
	}{
		{name: "success", path: "/auth/me", want: "/auth/me", status: http.StatusOK, level: "INFO"},
		{name: "client error", path: "/auth/me", want: "/auth/me", status: http.StatusBadRequest, level: "WARN"},
		{name: "server error", path: "/auth/me", want: "/auth/me", status: http.StatusInternalServerError, level: "ERROR"},
		{name: "health probe", path: "/healthz", want: "/healthz", status: http.StatusOK, level: "DEBUG"},
		{name: "failed probe", path: "/readyz", want: "/readyz", status: http.StatusServiceUnavailable, level: "ERROR"},
		{name: "unmatched path", path: "/missing", want: "/missing", status: http.StatusNotFound, level: "WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			handler := AccessLog(newTestLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("body"))
			}))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, nil)
			request.Header.Set("User-Agent", "probe/1")
			request.RemoteAddr = "192.0.2.4:5555"
			handler.ServeHTTP(httptest.NewRecorder(), request)

			all := records(t, &buf)
			if len(all) != 1 {
				t.Fatalf("log records = %d, want 1", len(all))
			}
			record := all[0]
			for key, want := range map[string]any{
				"msg":        "request handled",
				"level":      tc.level,
				"method":     http.MethodPost,
				"route":      tc.want,
				"status":     float64(tc.status),
				"bytes":      float64(4),
				"remote_ip":  "192.0.2.4",
				"user_agent": "probe/1",
			} {
				if got := record[key]; got != want {
					t.Errorf("%s = %#v, want %#v", key, got, want)
				}
			}
			if _, ok := record["duration_ms"].(float64); !ok {
				t.Errorf("duration_ms = %#v, want a number", record["duration_ms"])
			}
		})
	}
}

func TestAccessLogUsesRoutePattern(t *testing.T) {
	var buf bytes.Buffer
	router := chi.NewRouter()
	router.Use(AccessLog(newTestLogger(&buf)))
	router.Get("/users/{id}", func(http.ResponseWriter, *http.Request) {})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/users/42", nil))

	all := records(t, &buf)
	if len(all) != 1 {
		t.Fatalf("log records = %d, want 1", len(all))
	}
	if all[0]["route"] != "/users/{id}" {
		t.Errorf("route = %#v, want the route pattern", all[0]["route"])
	}
	if all[0]["status"] != float64(http.StatusOK) {
		t.Errorf("status = %#v, want 200 when the handler writes no status", all[0]["status"])
	}
}

func TestAccessLogTruncatesLongPath(t *testing.T) {
	var buf bytes.Buffer
	handler := AccessLog(newTestLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	path := "/" + strings.Repeat("x", 2*maxRouteLen)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	route, _ := records(t, &buf)[0]["route"].(string)
	if len(route) != maxRouteLen {
		t.Errorf("route length = %d, want %d", len(route), maxRouteLen)
	}
}

func TestAccessLogSeesHandlerAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	handler := RequestID(AccessLog(logger)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		AddAttrs(r.Context(), slog.String("user_id", "user-7"))
	})))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	all := records(t, &buf)
	if len(all) != 1 {
		t.Fatalf("log records = %d, want 1", len(all))
	}
	if all[0]["user_id"] != "user-7" {
		t.Errorf("user_id = %#v, want the value the handler added", all[0]["user_id"])
	}
}

func TestAddAttrsWithoutScope(t *testing.T) {
	AddAttrs(t.Context(), slog.String("user_id", "user-7"))
}
