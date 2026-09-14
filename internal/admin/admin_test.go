package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgxpool"
)

const adminTestPassword = "correct horse battery staple"

type adminTestEnv struct {
	api             humatest.TestAPI
	pool            *pgxpool.Pool
	queries         *db.Queries
	backendPublicID string
}

func newAdminTestEnv(t *testing.T, pool *pgxpool.Pool) adminTestEnv {
	t.Helper()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	authService, err := auth.New(pool, auth.Config{
		Logger: slog.New(slog.DiscardHandler), RateLimitClock: func() time.Time { return time.Now() },
	})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	New(pool, slog.New(slog.DiscardHandler)).Register(authService.Admin(api, "/api/v1/admin"))
	queries := db.New(pool)
	backend, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: id.New(id.StorageBackend), Name: "Local", Type: "local", Config: []byte(`{"root":"/tmp/stocat-test"}`),
		EncryptedSecrets: "test", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adminTestEnv{api: api, pool: pool, queries: queries, backendPublicID: backend.PublicID}
}

func (e adminTestEnv) register(t *testing.T, administrator bool) (string, db.User) {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": adminTestPassword,
	})
	requireStatus(t, response, http.StatusCreated)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	user, err := e.queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	if administrator {
		if _, err := e.queries.SetUserAdminByEmail(t.Context(), db.SetUserAdminByEmailParams{
			IsAdmin: true, Email: user.Email,
		}); err != nil {
			t.Fatal(err)
		}
		user.IsAdmin = true
	}
	return "Cookie: " + auth.CookieName + "=" + cookies[0].Value, user
}

func TestAdminUserQuotas(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newAdminTestEnv(t, pool)
	adminCookie, _ := env.register(t, true)
	_, target := env.register(t, false)
	ctx := t.Context()
	path := "/api/v1/admin/users/" + target.PublicID + "/quota"

	response := env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	users := decode[[]AdminUser](t, response)
	if len(users) != 2 {
		t.Fatalf("users = %+v, want two", users)
	}
	managed := findUser(t, users, target.PublicID)
	if managed.DefaultQuota.Mode != ModeInherit || managed.DefaultQuota.LimitBytes != nil || len(managed.BackendQuotas) != 0 {
		t.Fatalf("new user = %+v", managed)
	}

	response = env.api.PutCtx(ctx, path, adminCookie, map[string]any{
		"default_quota":  map[string]any{"mode": ModeLimited, "limit_bytes": 10 << 30},
		"backend_quotas": []map[string]any{{"backend_id": env.backendPublicID, "mode": ModeLimited, "limit_bytes": 1 << 30}},
	})
	requireStatus(t, response, http.StatusOK)
	requireQuotas(t, decode[AdminUser](t, response), Quota{Mode: ModeLimited, LimitBytes: ptr(10 << 30)},
		[]BackendQuota{{BackendID: env.backendPublicID, Mode: ModeLimited, LimitBytes: ptr(1 << 30)}})

	response = env.api.PutCtx(ctx, path, adminCookie, map[string]any{
		"default_quota":  map[string]any{"mode": ModeUnlimited},
		"backend_quotas": []map[string]any{{"backend_id": "missing", "mode": ModeUnlimited}},
	})
	requireStatus(t, response, http.StatusNotFound)
	response = env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	requireQuotas(t, findUser(t, decode[[]AdminUser](t, response), target.PublicID),
		Quota{Mode: ModeLimited, LimitBytes: ptr(10 << 30)},
		[]BackendQuota{{BackendID: env.backendPublicID, Mode: ModeLimited, LimitBytes: ptr(1 << 30)}})

	response = env.api.PutCtx(ctx, path, adminCookie, map[string]any{
		"default_quota":  map[string]any{"mode": ModeInherit},
		"backend_quotas": []map[string]any{{"backend_id": env.backendPublicID, "mode": ModeUnlimited}},
	})
	requireStatus(t, response, http.StatusOK)
	response = env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	requireQuotas(t, findUser(t, decode[[]AdminUser](t, response), target.PublicID),
		Quota{Mode: ModeInherit}, []BackendQuota{{BackendID: env.backendPublicID, Mode: ModeUnlimited}})

	response = env.api.PutCtx(ctx, path, adminCookie, map[string]any{
		"default_quota":  map[string]any{"mode": ModeUnlimited},
		"backend_quotas": []map[string]any{{"backend_id": env.backendPublicID, "mode": ModeInherit}},
	})
	requireStatus(t, response, http.StatusOK)
	response = env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	requireQuotas(t, findUser(t, decode[[]AdminUser](t, response), target.PublicID), Quota{Mode: ModeUnlimited}, nil)
}

func TestAdminQuotaSettings(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newAdminTestEnv(t, pool)
	adminCookie, _ := env.register(t, true)
	ctx := t.Context()

	response := env.api.GetCtx(ctx, "/api/v1/admin/quota", adminCookie)
	requireStatus(t, response, http.StatusOK)
	if settings := decode[QuotaSettings](t, response); settings.DefaultQuota.Mode != ModeUnlimited {
		t.Fatalf("initial settings = %+v", settings)
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/quota", adminCookie, map[string]any{
		"default_quota": map[string]any{"mode": ModeLimited, "limit_bytes": 5 << 30},
	})
	requireStatus(t, response, http.StatusOK)
	response = env.api.GetCtx(ctx, "/api/v1/admin/quota", adminCookie)
	requireStatus(t, response, http.StatusOK)
	if settings := decode[QuotaSettings](t, response); settings.DefaultQuota.Mode != ModeLimited ||
		settings.DefaultQuota.LimitBytes == nil || *settings.DefaultQuota.LimitBytes != 5<<30 {
		t.Fatalf("stored settings = %+v", settings)
	}

	for _, body := range []map[string]any{
		{"mode": ModeInherit},
		{"mode": ModeLimited},
		{"mode": ModeUnlimited, "limit_bytes": 1},
		{"mode": ModeLimited, "limit_bytes": -1},
	} {
		response = env.api.PutCtx(ctx, "/api/v1/admin/quota", adminCookie, map[string]any{"default_quota": body})
		requireStatus(t, response, http.StatusUnprocessableEntity)
	}
}

func TestAdminQuotaAuthorizationAndErrors(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newAdminTestEnv(t, pool)
	adminCookie, admin := env.register(t, true)
	memberCookie, member := env.register(t, false)
	ctx := t.Context()
	unlimited := map[string]any{"default_quota": map[string]any{"mode": ModeUnlimited}, "backend_quotas": []any{}}

	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/users"), http.StatusUnauthorized)
	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/users", memberCookie), http.StatusForbidden)
	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/quota", memberCookie), http.StatusForbidden)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/quota", memberCookie, unlimited), http.StatusForbidden)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/users/"+admin.PublicID+"/quota", memberCookie, unlimited),
		http.StatusForbidden)

	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/users/missing/quota", adminCookie, unlimited), http.StatusNotFound)
	memberPath := "/api/v1/admin/users/" + member.PublicID + "/quota"
	requireStatus(t, env.api.PutCtx(ctx, memberPath, adminCookie, map[string]any{
		"default_quota": map[string]any{"mode": ModeLimited, "limit_bytes": -1}, "backend_quotas": []any{},
	}), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.PutCtx(ctx, memberPath, adminCookie, map[string]any{
		"default_quota": map[string]any{"mode": ModeUnlimited},
		"backend_quotas": []map[string]any{
			{"backend_id": env.backendPublicID, "mode": ModeUnlimited},
			{"backend_id": env.backendPublicID, "mode": ModeLimited, "limit_bytes": 1},
		},
	}), http.StatusUnprocessableEntity)
}

func requireQuotas(t *testing.T, user AdminUser, defaultQuota Quota, backendQuotas []BackendQuota) {
	t.Helper()
	if !sameQuota(user.DefaultQuota.Mode, user.DefaultQuota.LimitBytes, defaultQuota.Mode, defaultQuota.LimitBytes) ||
		len(user.BackendQuotas) != len(backendQuotas) {
		t.Fatalf("quotas = %+v, want default %+v and overrides %+v", user, defaultQuota, backendQuotas)
	}
	for i, want := range backendQuotas {
		got := user.BackendQuotas[i]
		if got.BackendID != want.BackendID || !sameQuota(got.Mode, got.LimitBytes, want.Mode, want.LimitBytes) {
			t.Fatalf("override %d = %+v, want %+v", i, got, want)
		}
	}
}

func sameQuota(mode string, limit *int64, wantMode string, wantLimit *int64) bool {
	if mode != wantMode || (limit == nil) != (wantLimit == nil) {
		return false
	}
	return limit == nil || *limit == *wantLimit
}

func ptr(value int64) *int64 {
	return &value
}

func findUser(t *testing.T, users []AdminUser, publicID string) AdminUser {
	t.Helper()
	for _, user := range users {
		if user.ID == publicID {
			return user
		}
	}
	t.Fatalf("user %s not found in %+v", publicID, users)
	return AdminUser{}
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
