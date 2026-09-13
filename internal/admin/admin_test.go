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
	api       humatest.TestAPI
	pool      *pgxpool.Pool
	queries   *db.Queries
	backendID int64
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
	return adminTestEnv{api: api, pool: pool, queries: queries, backendID: backend.ID}
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

func (e adminTestEnv) createLibrary(t *testing.T, ownerID int64, name string) string {
	t.Helper()
	publicID := id.New(id.Library)
	err := db.InTx(t.Context(), e.pool, func(queries *db.Queries) error {
		libraryID, err := queries.NextLibraryID(t.Context())
		if err != nil {
			return err
		}
		rootID, err := queries.NextNodeID(t.Context())
		if err != nil {
			return err
		}
		if _, err := queries.CreateLibrary(t.Context(), db.CreateLibraryParams{
			ID: libraryID, PublicID: publicID, OwnerID: ownerID, BackendID: e.backendID,
			RootNodeID: rootID, Name: name, EncryptionMode: "none",
		}); err != nil {
			return err
		}
		_, err = queries.CreateNodeWithID(t.Context(), db.CreateNodeWithIDParams{
			ID: rootID, PublicID: id.New(id.Node), LibraryID: libraryID, Kind: "folder",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return publicID
}

func TestAdminUserQuotas(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newAdminTestEnv(t, pool)
	adminCookie, _ := env.register(t, true)
	_, target := env.register(t, false)
	libraryID := env.createLibrary(t, target.ID, "Documents")
	ctx := t.Context()

	response := env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	users := decode[[]AdminUser](t, response)
	if len(users) != 2 {
		t.Fatalf("users = %+v, want two", users)
	}
	managed := findUser(t, users, target.PublicID)
	if managed.DefaultQuotaMB != nil || len(managed.Libraries) != 1 {
		t.Fatalf("new user = %+v", managed)
	}
	if managed.Libraries[0].ID != libraryID || managed.Libraries[0].QuotaMB != nil {
		t.Fatalf("new library = %+v", managed.Libraries[0])
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/users/"+target.PublicID+"/quota", adminCookie, map[string]any{
		"default_quota_mb": 10240,
		"libraries":        []map[string]any{{"id": libraryID, "quota_mb": 1024}},
	})
	requireStatus(t, response, http.StatusOK)
	if user := decode[AdminUser](t, response); user.DefaultQuotaMB == nil || *user.DefaultQuotaMB != 10240 ||
		len(user.Libraries) != 1 || user.Libraries[0].QuotaMB == nil || *user.Libraries[0].QuotaMB != 1024 {
		t.Fatalf("updated user = %+v", user)
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/users/"+target.PublicID+"/quota", adminCookie, map[string]any{
		"default_quota_mb": 20480,
		"libraries": []map[string]any{
			{"id": libraryID, "quota_mb": 2048},
			{"id": "missing", "quota_mb": 1},
		},
	})
	requireStatus(t, response, http.StatusNotFound)

	response = env.api.GetCtx(ctx, "/api/v1/admin/users", adminCookie)
	requireStatus(t, response, http.StatusOK)
	managed = findUser(t, decode[[]AdminUser](t, response), target.PublicID)
	if managed.DefaultQuotaMB == nil || *managed.DefaultQuotaMB != 10240 ||
		managed.Libraries[0].QuotaMB == nil || *managed.Libraries[0].QuotaMB != 1024 {
		t.Fatalf("stored quotas = %+v", managed)
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/libraries/"+libraryID+"/quota", adminCookie, map[string]any{
		"quota_mb": nil,
	})
	requireStatus(t, response, http.StatusOK)
	if library := decode[LibraryQuota](t, response); library.QuotaMB != nil {
		t.Fatalf("cleared library = %+v", library)
	}
}

func TestAdminQuotaAuthorizationAndErrors(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newAdminTestEnv(t, pool)
	adminCookie, admin := env.register(t, true)
	memberCookie, member := env.register(t, false)
	libraryID := env.createLibrary(t, member.ID, "Documents")
	ctx := t.Context()

	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/users"), http.StatusUnauthorized)
	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/users", memberCookie), http.StatusForbidden)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/users/"+admin.PublicID+"/quota", memberCookie,
		map[string]any{"default_quota_mb": 1}), http.StatusForbidden)

	response := env.api.PutCtx(ctx, "/api/v1/admin/users/missing/quota", adminCookie,
		map[string]any{"default_quota_mb": 1})
	requireStatus(t, response, http.StatusNotFound)
	response = env.api.PutCtx(ctx, "/api/v1/admin/libraries/missing/quota", adminCookie,
		map[string]any{"quota_mb": 1})
	requireStatus(t, response, http.StatusNotFound)
	response = env.api.PutCtx(ctx, "/api/v1/admin/libraries/"+libraryID+"/quota", memberCookie,
		map[string]any{"quota_mb": 1})
	requireStatus(t, response, http.StatusForbidden)
	response = env.api.PutCtx(ctx, "/api/v1/admin/users/"+member.PublicID+"/quota", adminCookie,
		map[string]any{"default_quota_mb": -1})
	requireStatus(t, response, http.StatusUnprocessableEntity)
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
