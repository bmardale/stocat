package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestFileMetadataAndContent(t *testing.T) {
	pool := testutil.NewPostgres(t)
	root := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	authService, err := auth.New(pool, auth.Config{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	protected := authService.Protected(api, "/api/v1")
	libraries.New(pool, log).Register(protected)
	stores := storage.New(pool, storage.Config{Logger: log})
	New(pool, stores, log).Register(protected)

	queries := db.New(pool)
	backendID := id.New(id.StorageBackend)
	if _, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: backendID, Name: "Local", Type: storage.TypeLocal,
		Config: []byte(`{"root":` + quote(root) + `}`), EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	cookie, email := register(t, api)
	owner, err := queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	libraryResponse := api.Post("/api/v1/libraries", cookie, map[string]any{
		"name": "Documents", "backend_id": backendID, "encryption_mode": libraries.EncryptionNone,
	})
	requireFileStatus(t, libraryResponse, http.StatusCreated)
	var library libraries.Library
	decodeFile(t, libraryResponse, &library)
	libraryRow, err := queries.GetLibraryByPublicIDAndOwner(t.Context(), db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: library.ID, OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("stream this content")
	checksum := sha256.Sum256(content)
	objectKey := "blobs/test/content"
	store, err := stores.ObjectStore(t.Context(), backendID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), objectKey, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := queries.CreateBlob(t.Context(), db.CreateBlobParams{
		PublicID: id.New(id.Blob), LibraryID: libraryRow.ID, SizeBytes: int64(len(content)),
		CiphertextSha256: checksum[:], DedupFingerprint: checksum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := queries.CreateBlobLocation(t.Context(), db.CreateBlobLocationParams{
		PublicID: id.New(id.BlobLocation), BlobID: blob.ID, LibraryID: libraryRow.ID,
		BackendID: libraryRow.BackendID, ObjectKey: objectKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.MarkBlobLocationAvailable(t.Context(), location.ID); err != nil {
		t.Fatal(err)
	}
	node, err := queries.CreateUploadFileNode(t.Context(), db.CreateUploadFileNodeParams{
		PublicID: id.New(id.Node), LibraryID: libraryRow.ID,
		ParentID: pgtype.Int8{Int64: libraryRow.RootNodeID, Valid: true},
		Name:     pgtype.Text{String: "notes.txt", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := queries.CreateFileVersion(t.Context(), db.CreateFileVersionParams{
		PublicID: id.New(id.FileVersion), NodeID: node.ID, LibraryID: libraryRow.ID,
		Ordinal: 1, BlobID: blob.ID, SizeBytes: int64(len(content)), ContentSha256: checksum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.UpdateNodeCurrentVersion(t.Context(), db.UpdateNodeCurrentVersionParams{
		ID: node.ID, CurrentVersionID: pgtype.Int8{Int64: version.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	requireFileStatus(t, api.Get("/api/v1/files/"+node.PublicID), http.StatusUnauthorized)
	metadataResponse := api.Get("/api/v1/files/"+node.PublicID, cookie)
	requireFileStatus(t, metadataResponse, http.StatusOK)
	var metadata FileDetails
	decodeFile(t, metadataResponse, &metadata)
	if metadata.ID != node.PublicID || metadata.VersionID != version.PublicID || metadata.Size != int64(len(content)) || metadata.ContentURL == "" {
		t.Fatalf("metadata = %+v", metadata)
	}

	response := api.Get(metadata.ContentURL+"?disposition=inline", cookie)
	requireFileStatus(t, response, http.StatusOK)
	if !bytes.Equal(response.Body.Bytes(), content) || response.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("content = %q, headers = %v", response.Body, response.Header())
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "inline") || !strings.Contains(got, "notes.txt") {
		t.Fatalf("content disposition = %q", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("content security policy = %q", got)
	}

	response = api.Do(http.MethodGet, metadata.ContentURL, cookie, "Range: bytes=2-7")
	requireFileStatus(t, response, http.StatusPartialContent)
	if response.Body.String() != "ream t" || response.Header().Get("Content-Range") != "bytes 2-7/19" {
		t.Fatalf("range body = %q, header = %q", response.Body, response.Header().Get("Content-Range"))
	}
	response = api.Do(http.MethodGet, metadata.ContentURL, cookie, "Range: bytes=-7")
	requireFileStatus(t, response, http.StatusPartialContent)
	if response.Body.String() != "content" {
		t.Fatalf("suffix body = %q", response.Body)
	}
	response = api.Do(http.MethodGet, metadata.ContentURL, cookie, "Range: bytes=99-100")
	requireFileStatus(t, response, http.StatusRequestedRangeNotSatisfiable)
	if got := response.Header().Get("Content-Range"); got != "bytes */19" {
		t.Fatalf("unsatisfiable content range = %q", got)
	}
}

func TestPresentation(t *testing.T) {
	named := func(name string) pgtype.Text { return pgtype.Text{String: name, Valid: true} }
	tests := []struct {
		name        string
		file        pgtype.Text
		requested   string
		filename    string
		disposition string
	}{
		{name: "text", file: named("notes.txt"), requested: "inline", filename: "notes.txt", disposition: "inline"},
		{name: "uppercase image", file: named("photo.PNG"), requested: "inline", filename: "photo.PNG", disposition: "inline"},
		{name: "pdf", file: named("paper.pdf"), requested: "inline", filename: "paper.pdf", disposition: "inline"},
		{name: "html", file: named("page.html"), requested: "inline", filename: "page.html", disposition: "attachment"},
		{name: "svg", file: named("logo.svg"), requested: "inline", filename: "logo.svg", disposition: "attachment"},
		{name: "no extension", file: named("README"), requested: "inline", filename: "README", disposition: "attachment"},
		{name: "attachment", file: named("photo.png"), requested: "attachment", filename: "photo.png", disposition: "attachment"},
		{name: "encrypted name", requested: "inline", filename: "download.bin", disposition: "attachment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filename, _, disposition := presentation(test.file, test.requested)
			if filename != test.filename || disposition != test.disposition {
				t.Fatalf("presentation = %q, %q; want %q, %q", filename, disposition, test.filename, test.disposition)
			}
		})
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		size   int64
		offset int64
		length int64
		fail   bool
	}{
		{name: "all", size: 10},
		{name: "bounded", value: "bytes=2-5", size: 10, offset: 2, length: 4},
		{name: "open", value: "bytes=7-", size: 10, offset: 7, length: 3},
		{name: "suffix", value: "bytes=-4", size: 10, offset: 6, length: 4},
		{name: "clamped", value: "bytes=8-20", size: 10, offset: 8, length: 2},
		{name: "multiple", value: "bytes=0-1,3-4", size: 10, fail: true},
		{name: "past end", value: "bytes=10-", size: 10, fail: true},
		{name: "empty", value: "bytes=0-0", size: 0, fail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRange(test.value, test.size)
			if test.fail {
				if err == nil {
					t.Fatalf("parseRange(%q, %d) succeeded", test.value, test.size)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.value == "" {
				if got != nil {
					t.Fatalf("range = %+v", got)
				}
				return
			}
			if got.Offset != test.offset || got.Length != test.length {
				t.Fatalf("range = %+v, want offset %d and length %d", got, test.offset, test.length)
			}
		})
	}
}

func register(t *testing.T, api humatest.TestAPI) (string, string) {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := api.Post("/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": "correct horse battery staple",
	})
	requireFileStatus(t, response, http.StatusCreated)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	return "Cookie: " + auth.CookieName + "=" + cookies[0].Value, email
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func requireFileStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func decodeFile(t *testing.T, response *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), value); err != nil {
		t.Fatal(err)
	}
}
