package quota

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolve(t *testing.T) {
	limit := func(value int64) pgtype.Int8 { return pgtype.Int8{Int64: value, Valid: true} }
	tests := []struct {
		name    string
		global  pgtype.Int8
		user    Level
		backend Level
		want    pgtype.Int8
	}{
		{name: "no limit anywhere", want: pgtype.Int8{}},
		{name: "global default", global: limit(100), want: limit(100)},
		{name: "user default", global: limit(100), user: Level{Set: true, Limit: limit(50)}, want: limit(50)},
		{name: "user without limit", global: limit(100), user: Level{Set: true}, want: pgtype.Int8{}},
		{
			name: "backend override", global: limit(100), user: Level{Set: true, Limit: limit(50)},
			backend: Level{Set: true, Limit: limit(10)}, want: limit(10),
		},
		{
			name: "backend without limit", global: limit(100), user: Level{Set: true, Limit: limit(50)},
			backend: Level{Set: true}, want: pgtype.Int8{},
		},
		{name: "zero limit", global: limit(100), backend: Level{Set: true, Limit: limit(0)}, want: limit(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Resolve(test.global, test.user, test.backend); got != test.want {
				t.Fatalf("Resolve() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestExceeds(t *testing.T) {
	tests := []struct {
		name                          string
		stored, reserved, size, limit int64
		want                          bool
	}{
		{name: "fits exactly", stored: 40, reserved: 50, size: 10, limit: 100},
		{name: "one byte over", stored: 40, reserved: 50, size: 11, limit: 100, want: true},
		{name: "size over limit", size: 101, limit: 100, want: true},
		{name: "already over limit", stored: 150, limit: 100, want: true},
		{name: "large values do not overflow", stored: 1 << 62, reserved: 1 << 62, size: 1 << 62, limit: 1<<63 - 1, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exceeds(test.stored, test.reserved, test.size, test.limit); got != test.want {
				t.Fatalf("exceeds() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStorageUsageAndAdmission(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := t.Context()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	log := slog.New(slog.DiscardHandler)
	authService, err := auth.New(pool, auth.Config{Logger: log, RateLimitClock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	New(pool, log).Register(authService.Protected(api, "/api/v1"))
	queries := db.New(pool)

	email := strings.ToLower(id.New("test")) + "@example.com"
	response := api.PostCtx(ctx, "/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": "correct horse battery staple",
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("register status = %d: %s", response.Code, response.Body)
	}
	result := response.Result()
	cookies := result.Cookies()
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cookie := "Cookie: " + auth.CookieName + "=" + cookies[0].Value
	user, err := queries.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}

	local := createBackend(t, queries, "Local", "local")
	remote := createBackend(t, queries, "Backblaze", "s3")
	createBackend(t, queries, "Unused", "local")
	localLibrary, localRoot := createLibrary(t, pool, user.ID, local.ID, "Documents")
	remoteLibrary, _ := createLibrary(t, pool, user.ID, remote.ID, "Archive")
	createBlob(t, queries, localLibrary, "local", 600)
	createBlob(t, queries, remoteLibrary, "remote", 100)

	requireUsage(t, listUsage(t, api, cookie), []Usage{
		{Backend: backendOf(remote), UsedBytes: 100},
		{Backend: backendOf(local), UsedBytes: 600},
	})

	if _, err := queries.UpdateQuotaSettings(ctx, pgtype.Int8{Int64: 1000, Valid: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserBackendQuota(ctx, db.CreateUserBackendQuotaParams{
		UserID: user.ID, BackendPublicID: remote.PublicID, LimitBytes: pgtype.Int8{Int64: 150, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	requireAdmit(t, queries, user.ID, local.ID, 400, nil)
	requireAdmit(t, queries, user.ID, local.ID, 401, ErrExceeded)
	requireAdmit(t, queries, user.ID, remote.ID, 50, nil)
	requireAdmit(t, queries, user.ID, remote.ID, 51, ErrExceeded)

	if _, err := queries.CreateUploadSession(ctx, db.CreateUploadSessionParams{
		PublicID: id.New(id.Upload), OwnerID: user.ID, LibraryID: localLibrary, ParentID: localRoot,
		Name: pgtype.Text{String: "reserved.bin", Valid: true}, DeclaredSize: 300,
		StagingKey: id.New("staging"), DestinationKey: id.New("destination"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	requireAdmit(t, queries, user.ID, local.ID, 100, nil)
	requireAdmit(t, queries, user.ID, local.ID, 101, ErrExceeded)
	requireUsage(t, listUsage(t, api, cookie), []Usage{
		{Backend: backendOf(remote), UsedBytes: 100, LimitBytes: ptr(150)},
		{Backend: backendOf(local), UsedBytes: 900, LimitBytes: ptr(1000)},
	})

	if err := queries.CreateUserDefaultQuota(ctx, db.CreateUserDefaultQuotaParams{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	requireAdmit(t, queries, user.ID, local.ID, 1<<40, nil)
	requireAdmit(t, queries, user.ID, remote.ID, 51, ErrExceeded)
	requireUsage(t, listUsage(t, api, cookie), []Usage{
		{Backend: backendOf(remote), UsedBytes: 100, LimitBytes: ptr(150)},
		{Backend: backendOf(local), UsedBytes: 900},
	})
}

func createBackend(t *testing.T, queries *db.Queries, name, backendType string) db.StorageBackend {
	t.Helper()
	backend, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: id.New(id.StorageBackend), Name: name, Type: backendType, Config: []byte(`{}`),
		EncryptedSecrets: "test", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func createLibrary(t *testing.T, pool *pgxpool.Pool, ownerID, backendID int64, name string) (int64, int64) {
	t.Helper()
	var libraryID, rootID int64
	err := db.InTx(t.Context(), pool, func(queries *db.Queries) error {
		var err error
		if libraryID, err = queries.NextLibraryID(t.Context()); err != nil {
			return err
		}
		if rootID, err = queries.NextNodeID(t.Context()); err != nil {
			return err
		}
		if _, err := queries.CreateLibrary(t.Context(), db.CreateLibraryParams{
			ID: libraryID, PublicID: id.New(id.Library), OwnerID: ownerID, BackendID: backendID,
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
	return libraryID, rootID
}

func createBlob(t *testing.T, queries *db.Queries, libraryID int64, content string, size int64) {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	if _, err := queries.CreateBlob(t.Context(), db.CreateBlobParams{
		PublicID: id.New(id.Blob), LibraryID: libraryID, SizeBytes: size,
		CiphertextSha256: sum[:], DedupFingerprint: sum[:],
	}); err != nil {
		t.Fatal(err)
	}
}

func listUsage(t *testing.T, api humatest.TestAPI, cookie string) []Usage {
	t.Helper()
	response := api.GetCtx(t.Context(), "/api/v1/storage-usage", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("usage status = %d: %s", response.Code, response.Body)
	}
	var usage []Usage
	if err := json.Unmarshal(response.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	return usage
}

func requireUsage(t *testing.T, got, want []Usage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Backend != want[i].Backend || got[i].UsedBytes != want[i].UsedBytes ||
			(got[i].LimitBytes == nil) != (want[i].LimitBytes == nil) ||
			(got[i].LimitBytes != nil && *got[i].LimitBytes != *want[i].LimitBytes) {
			t.Fatalf("usage %d = %+v (limit %v), want %+v (limit %v)", i, got[i], got[i].LimitBytes, want[i], want[i].LimitBytes)
		}
	}
}

func requireAdmit(t *testing.T, queries *db.Queries, userID, backendID, size int64, want error) {
	t.Helper()
	if err := Admit(t.Context(), queries, userID, backendID, size); !errors.Is(err, want) {
		t.Fatalf("Admit(%d) = %v, want %v", size, err, want)
	}
}

func backendOf(backend db.StorageBackend) libraries.LibraryBackend {
	return libraries.LibraryBackend{ID: backend.PublicID, Name: backend.Name, Type: backend.Type}
}

func ptr(value int64) *int64 {
	return &value
}
