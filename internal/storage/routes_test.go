package storage

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/db/migrations"
	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/crypt"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const (
	testPassword = "correct horse battery staple"
	testSecret   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

type testEnv struct {
	api       humatest.TestAPI
	queries   *db.Queries
	encrypter *crypt.Encrypter
	admin     string
	user      string
}

func newTestEnv(t *testing.T, pool *pgxpool.Pool, encrypter *crypt.Encrypter) testEnv {
	t.Helper()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	now := time.Now()
	authService, err := auth.New(pool, auth.Config{Logger: slog.New(slog.DiscardHandler), RateLimitClock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	New(pool, Config{Encrypter: encrypter, Logger: slog.New(slog.DiscardHandler)}).Register(authService.Admin(api, "/api/v1/admin"))
	env := testEnv{api: api, queries: db.New(pool), encrypter: encrypter}
	env.admin = env.signUp(t, true)
	env.user = env.signUp(t, false)
	return env
}

func newEncrypter(t *testing.T, previous ...crypt.Key) *crypt.Encrypter {
	t.Helper()
	var key crypt.Key
	if err := key.UnmarshalText([]byte(crypt.GenerateKey())); err != nil {
		t.Fatal(err)
	}
	encrypter, err := crypt.New(key, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return encrypter
}

// signUp registers a user and returns a Cookie header for the user.
func (e testEnv) signUp(t *testing.T, admin bool) string {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", map[string]string{"name": "Test", "email": email, "password": testPassword})
	requireStatus(t, response, http.StatusCreated)
	if _, err := e.queries.SetUserAdminByEmail(t.Context(), db.SetUserAdminByEmailParams{Email: email, IsAdmin: admin}); err != nil {
		t.Fatal(err)
	}
	result := response.Result()
	defer func() {
		if err := result.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one session cookie", cookies)
	}
	return "Cookie: " + auth.CookieName + "=" + cookies[0].Value
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
		t.Fatalf("decode %s: %v", response.Body, err)
	}
	return value
}

// s3Body returns an update body. Use newS3Body for a create body.
func s3Body(name, accessKeyID, secret string) map[string]any {
	return map[string]any{
		"name": name, "enabled": true,
		"s3": map[string]any{
			"endpoint": "http://rustfs:9000", "region": "us-east-1", "bucket": "stocat", "prefix": "objects",
			"force_path_style": true, "access_key_id": accessKeyID, "secret_access_key": secret,
		},
	}
}

func newS3Body(name, accessKeyID, secret string) map[string]any {
	body := s3Body(name, accessKeyID, secret)
	body["type"] = TypeS3
	return body
}

func TestStorageBackends(t *testing.T) {
	pool := testutil.NewPostgres(t)
	t.Run("authorization", func(t *testing.T) { testAuthorization(t, pool) })
	t.Run("lifecycle", func(t *testing.T) { testLifecycle(t, pool) })
	t.Run("credentials", func(t *testing.T) { testCredentials(t, pool) })
	t.Run("validation", func(t *testing.T) { testValidation(t, pool) })
	t.Run("connection checks", func(t *testing.T) { testConnectionChecks(t, pool, testutil.NewRustFS(t)) })
	t.Run("migration rollback", func(t *testing.T) { testMigrationRollback(t, pool) })
}

func testAuthorization(t *testing.T, pool *pgxpool.Pool) {
	env := newTestEnv(t, pool, newEncrypter(t))
	for _, tc := range []struct {
		name   string
		cookie []any
		status int
	}{
		{"guest", nil, http.StatusUnauthorized},
		{"user", []any{env.user}, http.StatusForbidden},
		{"admin", []any{env.admin}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireStatus(t, env.api.GetCtx(t.Context(), "/api/v1/admin/storage-backends", tc.cookie...), tc.status)
			create := append(tc.cookie, map[string]any{"name": "Denied " + tc.name, "type": TypeLocal, "enabled": true, "local": map[string]string{"root": "/srv"}})
			if tc.status != http.StatusOK {
				requireStatus(t, env.api.PostCtx(t.Context(), "/api/v1/admin/storage-backends", create...), tc.status)
			}
		})
	}
}

func testLifecycle(t *testing.T, pool *pgxpool.Pool) {
	env := newTestEnv(t, pool, newEncrypter(t))
	ctx := t.Context()

	response := env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin,
		map[string]any{"name": " Local disk ", "type": TypeLocal, "enabled": true, "local": map[string]string{"root": "/srv/stocat/"}})
	requireStatus(t, response, http.StatusCreated)
	local := decode[Backend](t, response)
	if !id.Valid(id.StorageBackend, local.ID) || local.Name != "Local disk" || local.Local == nil || local.Local.Root != "/srv/stocat" || local.S3 != nil {
		t.Fatalf("unexpected backend: %+v", local)
	}

	response = env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin,
		map[string]any{"name": "LOCAL DISK", "type": TypeLocal, "enabled": true, "local": map[string]string{"root": "/srv/other"}})
	requireStatus(t, response, http.StatusConflict)

	response = env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin, newS3Body("Archive", "AKIAEXAMPLE", testSecret))
	requireStatus(t, response, http.StatusCreated)
	archive := decode[Backend](t, response)

	response = env.api.GetCtx(ctx, "/api/v1/admin/storage-backends", env.admin)
	requireStatus(t, response, http.StatusOK)
	if list := decode[[]Backend](t, response); len(list) != 2 || list[0].ID != archive.ID || list[1].ID != local.ID {
		t.Fatalf("list = %+v, want Archive and Local disk", list)
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin,
		map[string]any{"name": "Archive", "enabled": false, "local": map[string]string{"root": "/srv/stocat"}})
	requireStatus(t, response, http.StatusConflict)

	response = env.api.PutCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin,
		map[string]any{"name": "Local disk", "enabled": false, "local": map[string]string{"root": "/data"}})
	requireStatus(t, response, http.StatusOK)
	if updated := decode[Backend](t, response); updated.Enabled || updated.Local.Root != "/data" || updated.Type != TypeLocal {
		t.Fatalf("unexpected update: %+v", updated)
	}
	response = env.api.GetCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin)
	requireStatus(t, response, http.StatusOK)
	if got := decode[Backend](t, response); got.Enabled || got.Local.Root != "/data" {
		t.Fatalf("update was not stored: %+v", got)
	}

	response = env.api.PutCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin, s3Body("Local disk", "AKIAEXAMPLE", testSecret))
	requireStatus(t, response, http.StatusUnprocessableEntity)

	requireStatus(t, env.api.DeleteCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin), http.StatusNoContent)
	requireStatus(t, env.api.DeleteCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin), http.StatusNotFound)
	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin), http.StatusNotFound)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID, env.admin,
		map[string]any{"name": "Gone", "enabled": true, "local": map[string]string{"root": "/data"}}), http.StatusNotFound)
	requireStatus(t, env.api.DeleteCtx(ctx, "/api/v1/admin/storage-backends/"+archive.ID, env.admin), http.StatusNoContent)
}

func testCredentials(t *testing.T, pool *pgxpool.Pool) {
	var oldKey crypt.Key
	if err := oldKey.UnmarshalText([]byte(crypt.GenerateKey())); err != nil {
		t.Fatal(err)
	}
	oldEncrypter, err := crypt.New(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t, pool, oldEncrypter)
	ctx := t.Context()

	response := env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin, newS3Body("Credentials", "AKIAEXAMPLE", testSecret))
	requireStatus(t, response, http.StatusCreated)
	if strings.Contains(response.Body.String(), testSecret) || strings.Contains(response.Body.String(), "AKIAEXAMPLE") {
		t.Fatalf("response exposes credentials: %s", response.Body)
	}
	backend := decode[Backend](t, response)
	requireCredentials(t, env, backend.ID, "AKIAEXAMPLE", testSecret)

	response = env.api.GetCtx(ctx, "/api/v1/admin/storage-backends", env.admin)
	if strings.Contains(response.Body.String(), testSecret) || strings.Contains(response.Body.String(), "access_key_id") {
		t.Fatalf("list exposes credentials: %s", response.Body)
	}

	path := "/api/v1/admin/storage-backends/" + backend.ID
	requireStatus(t, env.api.PutCtx(ctx, path, env.admin, s3Body("Credentials", "", "")), http.StatusOK)
	requireCredentials(t, env, backend.ID, "AKIAEXAMPLE", testSecret)

	requireStatus(t, env.api.PutCtx(ctx, path, env.admin, s3Body("Credentials", "AKIANEW", "")), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.PutCtx(ctx, path, env.admin, s3Body("Credentials", "AKIANEW", "new-secret")), http.StatusOK)
	requireCredentials(t, env, backend.ID, "AKIANEW", "new-secret")

	// A rotated key decrypts the stored credentials through the previous key and encrypts them again with the new key.
	rotated := newTestEnv(t, pool, newEncrypter(t, oldKey))
	requireStatus(t, rotated.api.PutCtx(ctx, path, rotated.admin, s3Body("Credentials", "", "")), http.StatusOK)
	requireCredentials(t, rotated, backend.ID, "AKIANEW", "new-secret")
	row, err := env.queries.GetStorageBackendByPublicID(ctx, backend.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldEncrypter.Decrypt(row.EncryptedSecrets); err == nil {
		t.Fatal("update did not encrypt the credentials with the new key")
	}

	lost := newTestEnv(t, pool, newEncrypter(t))
	response = lost.api.PutCtx(ctx, path, lost.admin, s3Body("Credentials", "", ""))
	requireStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(response.Body.String(), "cannot be decrypted") {
		t.Fatalf("unexpected error: %s", response.Body)
	}
	requireStatus(t, lost.api.PutCtx(ctx, path, lost.admin, s3Body("Credentials", "AKIALOST", "lost-secret")), http.StatusOK)
	requireCredentials(t, lost, backend.ID, "AKIALOST", "lost-secret")
}

func requireCredentials(t *testing.T, env testEnv, backendID, accessKeyID, secret string) {
	t.Helper()
	row, err := env.queries.GetStorageBackendByPublicID(t.Context(), backendID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.EncryptedSecrets, secret) || strings.Contains(string(row.Config), secret) || strings.Contains(string(row.Config), accessKeyID) {
		t.Fatal("database stores plaintext credentials")
	}
	plaintext, err := env.encrypter.Decrypt(row.EncryptedSecrets)
	if err != nil {
		t.Fatal(err)
	}
	var got s3Credentials
	if err := json.Unmarshal(plaintext, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != accessKeyID || got.SecretAccessKey != secret {
		t.Fatalf("credentials = %+v, want %s", got, accessKeyID)
	}
}

func testValidation(t *testing.T, pool *pgxpool.Pool) {
	env := newTestEnv(t, pool, newEncrypter(t))
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"blank name", map[string]any{"name": " ", "type": TypeLocal, "enabled": true, "local": map[string]string{"root": "/srv"}}},
		{"unknown type", map[string]any{"name": "FTP", "type": "ftp", "enabled": true}},
		{"missing settings", map[string]any{"name": "Missing", "type": TypeS3, "enabled": true}},
		{"relative root", map[string]any{"name": "Relative", "type": TypeLocal, "enabled": true, "local": map[string]string{"root": "srv"}}},
		{"missing credentials", newS3Body("No credentials", "", "")},
		{"long secret", newS3Body("Long secret", "AKIAEXAMPLE", strings.Repeat("s", 257)+testSecret)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := env.api.PostCtx(t.Context(), "/api/v1/admin/storage-backends", env.admin, tc.body)
			requireStatus(t, response, http.StatusUnprocessableEntity)
			if strings.Contains(response.Body.String(), testSecret) {
				t.Fatalf("error exposes the secret: %s", response.Body)
			}
		})
	}
}

func testMigrationRollback(t *testing.T, pool *pgxpool.Pool) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Down(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Version != 4 {
		t.Fatalf("rolled back migration %d, want 4", result.Source.Version)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("reapply migration: %v", err)
	}
	if _, err := db.New(pool).ListStorageBackends(t.Context()); err != nil {
		t.Fatalf("reapplied migration is not usable: %v", err)
	}
}

func testConnectionChecks(t *testing.T, pool *pgxpool.Pool, server testutil.S3) {
	env := newTestEnv(t, pool, newEncrypter(t))
	ctx := t.Context()
	config, keys := newTestBucket(t, server, "api-checks")
	settings := func(accessKeyID, secret string) map[string]any {
		return map[string]any{
			"endpoint": config.Endpoint, "region": config.Region, "bucket": config.Bucket, "prefix": "checks",
			"force_path_style": true, "access_key_id": accessKeyID, "secret_access_key": secret,
		}
	}
	requireCheck := func(response *httptest.ResponseRecorder, ok bool, message string) {
		t.Helper()
		requireStatus(t, response, http.StatusOK)
		if got := decode[ConnectionCheck](t, response); got.OK != ok || got.Message != message {
			t.Fatalf("check = %+v, want ok = %v and %q", got, ok, message)
		}
	}
	const works = "The server can write, read, and delete objects."

	response := env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin,
		map[string]any{"name": "Check disk", "type": TypeLocal, "enabled": true, "local": map[string]string{"root": t.TempDir()}})
	requireStatus(t, response, http.StatusCreated)
	local := decode[Backend](t, response)
	requireCheck(env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID+"/check", env.admin), true, works)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/"+local.ID+"/check", env.user), http.StatusForbidden)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/stb_01K4W9T5V8QK3M7ZB0YHXC2FNE/check", env.admin), http.StatusNotFound)

	requireCheck(env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/check", env.admin,
		map[string]any{"type": TypeLocal, "local": map[string]string{"root": filepath.Join(t.TempDir(), "missing")}}),
		false, "The directory does not exist.")

	response = env.api.PostCtx(ctx, "/api/v1/admin/storage-backends", env.admin,
		map[string]any{"name": "Check bucket", "type": TypeS3, "enabled": true, "s3": settings(keys.AccessKeyID, keys.SecretAccessKey)})
	requireStatus(t, response, http.StatusCreated)
	bucket := decode[Backend](t, response)
	requireCheck(env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/"+bucket.ID+"/check", env.admin), true, works)

	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"stored credentials", map[string]any{"id": bucket.ID, "type": TypeS3, "s3": settings("", "")}, http.StatusOK},
		{"changed endpoint", map[string]any{"id": bucket.ID, "type": TypeS3, "s3": func() map[string]any {
			body := settings("", "")
			body["endpoint"] = "https://attacker.example"
			return body
		}()}, http.StatusUnprocessableEntity},
		{"different type", map[string]any{"id": bucket.ID, "type": TypeLocal, "local": map[string]string{"root": "/srv"}}, http.StatusUnprocessableEntity},
		{"unknown backend", map[string]any{"id": "stb_01K4W9T5V8QK3M7ZB0YHXC2FNE", "type": TypeS3, "s3": settings("", "")}, http.StatusNotFound},
		{"missing credentials", map[string]any{"type": TypeS3, "s3": settings("", "")}, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/check", env.admin, tc.body)
			if tc.status == http.StatusOK {
				requireCheck(response, true, works)
				return
			}
			requireStatus(t, response, tc.status)
		})
	}

	response = env.api.PostCtx(ctx, "/api/v1/admin/storage-backends/check", env.admin,
		map[string]any{"type": TypeS3, "s3": settings(keys.AccessKeyID, testSecret)})
	requireCheck(response, false, "The secret access key is not correct.")
	if strings.Contains(response.Body.String(), testSecret) || strings.Contains(response.Body.String(), keys.AccessKeyID) {
		t.Fatalf("check exposes credentials: %s", response.Body)
	}

	lost := newTestEnv(t, pool, newEncrypter(t))
	requireCheck(lost.api.PostCtx(ctx, "/api/v1/admin/storage-backends/"+bucket.ID+"/check", lost.admin), false, undecryptableCredentials)
}
