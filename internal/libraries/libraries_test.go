package libraries

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

const libraryTestPassword = "correct horse battery staple"

type libraryTestEnv struct {
	api       humatest.TestAPI
	queries   *db.Queries
	backendID string
}

func newLibraryTestEnv(t *testing.T, pool *pgxpool.Pool) libraryTestEnv {
	t.Helper()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	authService, err := auth.New(pool, auth.Config{Logger: slog.New(slog.DiscardHandler), RateLimitClock: func() time.Time { return time.Now() }})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	New(pool, slog.New(slog.DiscardHandler)).Register(authService.Protected(api, "/api/v1"))
	queries := db.New(pool)
	backendID := id.New(id.StorageBackend)
	if _, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: backendID, Name: "Local", Type: "local", Config: []byte(`{"root":"/tmp/stocat-test"}`),
		EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return libraryTestEnv{api: api, queries: queries, backendID: backendID}
}

func (e libraryTestEnv) signUp(t *testing.T) string {
	t.Helper()
	cookie, _ := e.signUpWithID(t)
	return cookie
}

func (e libraryTestEnv) signUpWithID(t *testing.T) (string, int64) {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": libraryTestPassword,
	})
	requireLibraryStatus(t, response, http.StatusCreated)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	user, err := e.queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	return "Cookie: " + auth.CookieName + "=" + cookies[0].Value, user.ID
}

func TestPrivateLibraries(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newLibraryTestEnv(t, pool)
	owner := env.signUp(t)
	other := env.signUp(t)
	ctx := t.Context()

	requireLibraryStatus(t, env.api.GetCtx(ctx, "/api/v1/libraries"), http.StatusUnauthorized)
	response := env.api.PostCtx(ctx, "/api/v1/libraries", owner, map[string]any{
		"name": "Documents", "backend_id": env.backendID, "encryption_mode": EncryptionNone,
	})
	requireLibraryStatus(t, response, http.StatusCreated)
	plain := decodeLibrary[Library](t, response)
	if !id.Valid(id.Library, plain.ID) || !id.Valid(id.Node, plain.RootNodeID) || plain.Name != "Documents" || plain.Backend.ID != env.backendID {
		t.Fatalf("plain library = %+v", plain)
	}

	response = env.api.PostCtx(ctx, "/api/v1/libraries", owner, map[string]any{
		"name": "Private", "backend_id": env.backendID, "encryption_mode": EncryptionE2EE, "key_envelope": []byte("wrapped-key"),
	})
	requireLibraryStatus(t, response, http.StatusCreated)
	encrypted := decodeLibrary[Library](t, response)
	if encrypted.EncryptionMode != EncryptionE2EE || string(encrypted.KeyEnvelope) != "wrapped-key" {
		t.Fatalf("encrypted library = %+v", encrypted)
	}

	requireLibraryStatus(t, env.api.GetCtx(ctx, "/api/v1/libraries/"+plain.ID, other), http.StatusNotFound)
	response = env.api.GetCtx(ctx, "/api/v1/libraries", other)
	requireLibraryStatus(t, response, http.StatusOK)
	if libraries := decodeLibrary[[]Library](t, response); len(libraries) != 0 {
		t.Fatalf("other user libraries = %+v", libraries)
	}

	requireLibraryStatus(t, env.api.PostCtx(ctx, "/api/v1/libraries", owner, map[string]any{
		"name": "Documents", "backend_id": env.backendID, "encryption_mode": EncryptionNone,
	}), http.StatusConflict)
	requireLibraryStatus(t, env.api.PostCtx(ctx, "/api/v1/libraries", owner, map[string]any{
		"name": "Missing key", "backend_id": env.backendID, "encryption_mode": EncryptionE2EE,
	}), http.StatusUnprocessableEntity)
}

func TestLibraryFolders(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newLibraryTestEnv(t, pool)
	owner := env.signUp(t)
	ctx := t.Context()
	plain := createTestLibrary(t, env, owner, "Plain", EncryptionNone, nil)
	encrypted := createTestLibrary(t, env, owner, "Encrypted", EncryptionE2EE, []byte("wrapped"))

	response := env.api.PostCtx(ctx, "/api/v1/libraries/"+plain.ID+"/folders", owner, map[string]any{"name": "Archive"})
	requireLibraryStatus(t, response, http.StatusCreated)
	folder := decodeLibrary[Node](t, response)
	if folder.Name != "Archive" || folder.ParentID != plain.RootNodeID || !id.Valid(id.Node, folder.ID) {
		t.Fatalf("folder = %+v", folder)
	}
	requireLibraryStatus(t, env.api.PostCtx(ctx, "/api/v1/libraries/"+plain.ID+"/folders", owner,
		map[string]any{"name": "Archive"}), http.StatusConflict)
	requireLibraryStatus(t, env.api.PostCtx(ctx, "/api/v1/libraries/"+plain.ID+"/folders", owner,
		map[string]any{"name": "../escape"}), http.StatusUnprocessableEntity)

	token := []byte("01234567890123456789012345678901")
	response = env.api.PostCtx(ctx, "/api/v1/libraries/"+encrypted.ID+"/folders", owner, map[string]any{
		"encrypted_name": []byte("encrypted filename"), "name_token": token,
	})
	requireLibraryStatus(t, response, http.StatusCreated)
	secret := decodeLibrary[Node](t, response)
	if secret.Name != "" || string(secret.EncryptedName) != "encrypted filename" || string(secret.NameToken) != string(token) {
		t.Fatalf("encrypted folder = %+v", secret)
	}
	requireLibraryStatus(t, env.api.PostCtx(ctx, "/api/v1/libraries/"+encrypted.ID+"/folders", owner, map[string]any{
		"encrypted_name": []byte("different ciphertext"), "name_token": token,
	}), http.StatusConflict)

	response = env.api.GetCtx(ctx, "/api/v1/libraries/"+plain.ID+"/nodes", owner)
	requireLibraryStatus(t, response, http.StatusOK)
	page := decodeLibrary[nodesPage](t, response)
	if len(page.Items) != 1 || page.Items[0].ID != folder.ID {
		t.Fatalf("plain folder page = %+v", page)
	}
	requireLibraryStatus(t, env.api.GetCtx(ctx, "/api/v1/libraries/"+plain.ID+"/nodes?parent_id="+secret.ID, owner), http.StatusNotFound)
}

func TestBlobDeduplicationScope(t *testing.T) {
	pool := testutil.NewPostgres(t)
	env := newLibraryTestEnv(t, pool)
	owner, ownerID := env.signUpWithID(t)
	first := createTestLibrary(t, env, owner, "First", EncryptionNone, nil)
	second := createTestLibrary(t, env, owner, "Second", EncryptionNone, nil)
	firstRow, err := env.queries.GetLibraryByPublicIDAndOwner(t.Context(), db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: first.ID, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRow, err := env.queries.GetLibraryByPublicIDAndOwner(t.Context(), db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: second.ID, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := []byte("01234567890123456789012345678901")
	checksum := []byte("abcdefghijklmnopqrstuvwxyzABCDEF")
	create := func(t *testing.T, libraryID int64) error {
		t.Helper()
		_, err := env.queries.CreateBlob(t.Context(), db.CreateBlobParams{
			PublicID: id.New(id.Blob), LibraryID: libraryID, SizeBytes: 10,
			CiphertextSha256: checksum, DedupFingerprint: fingerprint,
		})
		return err
	}
	if err := create(t, firstRow.ID); err != nil {
		t.Fatal(err)
	}
	if err := create(t, firstRow.ID); err == nil {
		t.Fatal("duplicate fingerprint in one library succeeded")
	}
	if err := create(t, secondRow.ID); err != nil {
		t.Fatalf("same fingerprint in another library: %v", err)
	}
	found, err := env.queries.FindBlobByDedupFingerprint(t.Context(), db.FindBlobByDedupFingerprintParams{
		LibraryID: firstRow.ID, DedupFingerprint: fingerprint,
	})
	if err != nil || found.LibraryID != firstRow.ID {
		t.Fatalf("find deduplicated blob = %+v, %v", found, err)
	}
}

func createTestLibrary(t *testing.T, env libraryTestEnv, cookie, name, mode string, envelope []byte) Library {
	t.Helper()
	response := env.api.PostCtx(t.Context(), "/api/v1/libraries", cookie, map[string]any{
		"name": name, "backend_id": env.backendID, "encryption_mode": mode, "key_envelope": envelope,
	})
	requireLibraryStatus(t, response, http.StatusCreated)
	return decodeLibrary[Library](t, response)
}

func requireLibraryStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func decodeLibrary[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
