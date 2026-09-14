package activity

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/admin"
	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
)

const (
	testPassword  = "correct horse battery staple"
	testUserAgent = "User-Agent: activity-test"
	// humatest uses this client address for each request.
	testIP = "127.0.0.1"
)

type testEnv struct {
	api     humatest.TestAPI
	queries *db.Queries
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	pool := testutil.NewPostgres(t)
	log := slog.New(slog.DiscardHandler)
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	router := chi.NewRouter()
	router.Use(audit.Middleware)
	api := humatest.Wrap(t, humachi.New(router, cfg))
	authService, err := auth.New(pool, auth.Config{Logger: log, RateLimitClock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	adminGroup := authService.Admin(api, "/api/v1/admin")
	admin.New(pool, log).Register(adminGroup)
	service := New(pool, log)
	service.RegisterAdmin(adminGroup)
	service.Register(authService.Protected(api, "/api/v1"))
	return testEnv{api: api, queries: db.New(pool)}
}

func (e testEnv) register(t *testing.T, name, email string) (string, db.User) {
	t.Helper()
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", testUserAgent, map[string]string{
		"name": name, "email": email, "password": testPassword,
	})
	requireStatus(t, response, http.StatusCreated)
	result := response.Result()
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	user, err := e.queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	return "Cookie: " + auth.CookieName + "=" + cookies[0].Value, user
}

func (e testEnv) page(t *testing.T, path, cookie string) AuditEventPage {
	t.Helper()
	response := e.api.GetCtx(t.Context(), path, cookie)
	requireStatus(t, response, http.StatusOK)
	var page AuditEventPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestActivity(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()
	userCookie, user := env.register(t, "Ada", "ada@example.com")
	adminCookie, administrator := env.register(t, "Grace", "grace@example.com")
	if _, err := env.queries.SetUserAdminByEmail(ctx, db.SetUserAdminByEmailParams{
		Email: administrator.Email, IsAdmin: true,
	}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, env.api.PatchCtx(ctx, "/api/v1/auth/account", userCookie, testUserAgent, map[string]string{
		"name": "Ada Lovelace", "email": user.Email,
	}), http.StatusOK)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/users/"+user.PublicID+"/quota", adminCookie, testUserAgent, map[string]any{
		"default_quota": map[string]any{"mode": admin.ModeUnlimited}, "backend_quotas": []any{},
	}), http.StatusOK)

	own := env.page(t, "/api/v1/account-events", userCookie)
	requireActions(t, own.Items, audit.UserQuotaUpdated, audit.AccountUpdated, audit.AccountRegistered)
	quota, updated := own.Items[0], own.Items[1]
	if quota.Actor == nil || quota.Actor.Name != "Grace" || quota.Actor.Email != "" ||
		quota.IPAddress != "" || quota.UserAgent != "" || quota.Subject == nil || quota.Subject.ID != user.PublicID {
		t.Errorf("the user sees the quota event as %+v", quota)
	}
	if updated.Details.Name != "Ada Lovelace" || updated.Details.PreviousName != "Ada" || updated.Details.Email != "" ||
		updated.IPAddress != testIP || updated.UserAgent != "activity-test" || updated.Actor.Email != user.Email {
		t.Errorf("account event = %+v", updated)
	}

	first := env.page(t, "/api/v1/account-events?limit=2", userCookie)
	if len(first.Items) != 2 || first.NextCursor != updated.ID {
		t.Fatalf("first page = %+v", first)
	}
	rest := env.page(t, "/api/v1/account-events?limit=2&cursor="+first.NextCursor, userCookie)
	requireActions(t, rest.Items, audit.AccountRegistered)
	if rest.NextCursor != "" {
		t.Errorf("last page has cursor %q", rest.NextCursor)
	}

	all := env.page(t, "/api/v1/admin/audit-events", adminCookie)
	requireActions(t, all.Items, audit.UserQuotaUpdated, audit.AccountUpdated, audit.AccountRegistered, audit.AccountRegistered)
	filtered := env.page(t, "/api/v1/admin/audit-events?action=quota.user_updated&user="+user.PublicID, adminCookie)
	requireActions(t, filtered.Items, audit.UserQuotaUpdated)
	if event := filtered.Items[0]; event.Actor.Email != administrator.Email || event.IPAddress != testIP ||
		event.Details.QuotaMode != admin.ModeUnlimited || event.TargetID != user.PublicID {
		t.Errorf("the administrator sees the quota event as %+v", event)
	}
	future := url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	futurePage := env.page(t, "/api/v1/admin/audit-events?from="+future, adminCookie)
	if len(futurePage.Items) != 0 {
		t.Errorf("future audit filter returned %d events", len(futurePage.Items))
	}
	export := env.api.GetCtx(ctx, "/api/v1/admin/audit-events/export?action=quota.user_updated", adminCookie)
	requireStatus(t, export, http.StatusOK)
	if contentType := export.Header().Get("Content-Type"); contentType != "text/csv; charset=utf-8" {
		t.Errorf("export content type = %q", contentType)
	}
	if disposition := export.Header().Get("Content-Disposition"); disposition != `attachment; filename="audit-log.csv"` {
		t.Errorf("export disposition = %q", disposition)
	}
	if body := export.Body.String(); !strings.Contains(body, "created_at,actor,actor_email,action") ||
		!strings.Contains(body, string(audit.UserQuotaUpdated)) {
		t.Errorf("export body = %q", body)
	}

	for _, tc := range []struct {
		path, cookie string
		status       int
	}{
		{"/api/v1/account-events", "", http.StatusUnauthorized},
		{"/api/v1/account-events?cursor=aud_missing", userCookie, http.StatusUnprocessableEntity},
		{"/api/v1/admin/audit-events", userCookie, http.StatusForbidden},
		{"/api/v1/admin/audit-events?action=file.stolen", adminCookie, http.StatusUnprocessableEntity},
		{"/api/v1/admin/audit-events?user=usr_missing", adminCookie, http.StatusNotFound},
	} {
		requireStatus(t, env.api.GetCtx(ctx, tc.path, tc.cookie), tc.status)
	}
}

func requireActions(t *testing.T, events []AuditEvent, want ...audit.Action) {
	t.Helper()
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, string(event.Action))
	}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != string(want[i]) {
			t.Fatalf("actions = %v, want %v", got, want)
		}
	}
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}
