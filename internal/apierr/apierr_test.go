package apierr

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

// decode reads a problem body and fails the test when the body is not one.
func decode(t *testing.T, response *httptest.ResponseRecorder) Problem {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != ContentType {
		t.Errorf("Content-Type = %q, want %q", got, ContentType)
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode body: %v\nbody: %s", err, response.Body)
	}
	if problem.Status != response.Code {
		t.Errorf("body status = %d, want %d", problem.Status, response.Code)
	}
	if problem.Title == "" {
		t.Error("body has no title")
	}
	return problem
}

func TestNew(t *testing.T) {
	for _, tc := range []struct {
		name   string
		errs   []error
		detail int
	}{
		{name: "no details"},
		{name: "one detail", errs: []error{&huma.ErrorDetail{Location: "body.email", Message: "expected format email"}}, detail: 1},
		{name: "nil error is dropped", errs: []error{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem := New(http.StatusUnprocessableEntity, "validation failed", tc.errs...)
			if problem.Status != http.StatusUnprocessableEntity {
				t.Errorf("status = %d", problem.Status)
			}
			if problem.Title != http.StatusText(http.StatusUnprocessableEntity) {
				t.Errorf("title = %q", problem.Title)
			}
			if problem.Detail != "validation failed" {
				t.Errorf("detail = %q", problem.Detail)
			}
			if len(problem.Errors) != tc.detail {
				t.Errorf("errors = %d, want %d", len(problem.Errors), tc.detail)
			}
		})
	}
}

func TestProblemSatisfiesHuma(t *testing.T) {
	var status huma.StatusError = New(http.StatusTeapot, "short and stout")
	if status.GetStatus() != http.StatusTeapot {
		t.Errorf("GetStatus = %d", status.GetStatus())
	}
	if status.Error() != "short and stout" {
		t.Errorf("Error = %q", status.Error())
	}
	filter, ok := status.(huma.ContentTypeFilter)
	if !ok {
		t.Fatal("the problem does not filter the content type")
	}
	if got := filter.ContentType("application/json"); got != ContentType {
		t.Errorf("ContentType = %q, want %q", got, ContentType)
	}
}

func TestWriteHoldsRequestID(t *testing.T) {
	handler := o11y.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Write(w, r, http.StatusConflict, "the thing exists")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(o11y.RequestIDHeader, "req-9")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	got := decode(t, recorder)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got.RequestID != "req-9" {
		t.Errorf("request_id = %q, want %q", got.RequestID, "req-9")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing cache protection")
	}
}

func TestWriteWithoutRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	Write(recorder, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusNotFound, "gone")
	if problem := decode(t, recorder); problem.RequestID != "" {
		t.Errorf("request_id = %q, want an empty value", problem.RequestID)
	}
}

func TestRouterErrors(t *testing.T) {
	router := chi.NewRouter()
	router.Use(o11y.RequestID)
	router.NotFound(Handler(http.StatusNotFound, "This path does not exist."))
	router.MethodNotAllowed(MethodNotAllowed(router))
	router.Get("/things", func(http.ResponseWriter, *http.Request) {})
	router.Post("/things", func(http.ResponseWriter, *http.Request) {})

	for _, tc := range []struct {
		name, method, path, allow string
		status                    int
	}{
		{name: "unknown path", method: http.MethodGet, path: "/missing", status: http.StatusNotFound},
		{name: "wrong method", method: http.MethodDelete, path: "/things", status: http.StatusMethodNotAllowed, allow: "GET, POST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			problem := decode(t, recorder)
			if problem.RequestID == "" {
				t.Error("body has no request identifier")
			}
			if got := recorder.Header().Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
		})
	}
}

func TestRecoverer(t *testing.T) {
	var buf bytes.Buffer
	logger := o11y.NewLogger(o11y.LoggingConfig{Level: slog.LevelDebug}, &buf)
	handler := o11y.RequestID(Recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(o11y.RequestIDHeader, "req-2")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if problem := decode(t, recorder); problem.RequestID != "req-2" {
		t.Errorf("request_id = %q, want %q", problem.RequestID, "req-2")
	}

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode log output: %v\noutput: %s", err, buf.String())
	}
	if record["request_id"] != "req-2" || record["msg"] != "handler panic" {
		t.Errorf("record = %#v", record)
	}
	if stack, _ := record["stack"].(string); !strings.Contains(stack, "apierr.") {
		t.Errorf("stack = %q, want the call stack of the panic", stack)
	}
	if strings.Contains(recorder.Body.String(), "boom") {
		t.Error("the response body shows the panic value")
	}
}

func TestRecovererKeepsAbortHandler(t *testing.T) {
	var buf bytes.Buffer
	handler := Recoverer(o11y.NewLogger(o11y.LoggingConfig{Level: slog.LevelDebug}, &buf))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		}))
	defer func() {
		if value := recover(); value != http.ErrAbortHandler {
			t.Errorf("recovered %#v, want the abort error to pass through", value)
		}
		if buf.Len() != 0 {
			t.Errorf("the abort error went to the log: %s", buf.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
