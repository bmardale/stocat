package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func TestFileMetadataAndContent(t *testing.T) {
	f := newFixture(t)
	api, cookie := f.api, f.cookie
	content := []byte("stream this content")
	node, version := f.createFile(t, "notes.txt", f.storeBlob(t, "blobs/test/content", content))

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

func TestRenameFile(t *testing.T) {
	f := newFixture(t)
	blob := f.storeBlob(t, "blobs/test/rename", []byte("rename"))
	first, _ := f.createFile(t, "first.txt", blob)
	second, _ := f.createFile(t, "second.txt", blob)

	response := f.api.Patch("/api/v1/files/"+first.PublicID, f.cookie, map[string]any{"name": " renamed.txt "})
	requireFileStatus(t, response, http.StatusOK)
	var renamed libraries.Node
	decodeFile(t, response, &renamed)
	if renamed.ID != first.PublicID || renamed.Name != "renamed.txt" || renamed.ParentID != f.library.RootNodePublicID || renamed.LibraryID != f.library.PublicID {
		t.Fatalf("renamed = %+v", renamed)
	}

	tests := []struct {
		name   string
		fileID string
		cookie bool
		body   map[string]any
		status int
	}{
		{name: "anonymous", fileID: first.PublicID, body: map[string]any{"name": "other.txt"}, status: http.StatusUnauthorized},
		{name: "duplicate", fileID: second.PublicID, cookie: true, body: map[string]any{"name": "renamed.txt"}, status: http.StatusConflict},
		{name: "separator", fileID: first.PublicID, cookie: true, body: map[string]any{"name": "a/b"}, status: http.StatusUnprocessableEntity},
		{
			name: "encrypted name", fileID: first.PublicID, cookie: true,
			body:   map[string]any{"encrypted_name": []byte("name"), "name_token": make([]byte, 32)},
			status: http.StatusUnprocessableEntity,
		},
		{name: "missing", fileID: id.New(id.Node), cookie: true, body: map[string]any{"name": "other.txt"}, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []any{test.body}
			if test.cookie {
				args = []any{f.cookie, test.body}
			}
			requireFileStatus(t, f.api.Patch("/api/v1/files/"+test.fileID, args...), test.status)
		})
	}
}

func TestDeleteFile(t *testing.T) {
	f := newFixture(t)
	const sharedKey = "blobs/test/shared"
	shared := f.storeBlob(t, sharedKey, []byte("shared content"))
	first, _ := f.createFile(t, "first.txt", shared)
	second, secondVersion := f.createFile(t, "second.txt", shared)
	replacement := f.createUploadSession(t, pgtype.Int8{Int64: second.ID, Valid: true})
	published := f.createUploadSession(t, pgtype.Int8{})
	if err := f.queries.SetZeroLengthUploadReady(t.Context(), published.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.queries.SetPlainUploadFinalizing(t.Context(), published.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.queries.CompleteUploadSession(t.Context(), db.CompleteUploadSessionParams{
		ID:                 published.ID,
		PublishedNodeID:    pgtype.Int8{Int64: second.ID, Valid: true},
		PublishedVersionID: pgtype.Int8{Int64: secondVersion.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	requireFileStatus(t, f.api.Delete("/api/v1/files/"+first.PublicID), http.StatusUnauthorized)
	requireFileStatus(t, f.api.Delete("/api/v1/files/"+first.PublicID, f.cookie), http.StatusNoContent)
	requireFileStatus(t, f.api.Get("/api/v1/files/"+first.PublicID, f.cookie), http.StatusNotFound)
	requireFileStatus(t, f.api.Delete("/api/v1/files/"+first.PublicID, f.cookie), http.StatusNotFound)
	if stored := f.storedBytes(t); stored != shared.SizeBytes {
		t.Fatalf("stored bytes after trashing = %d, want %d", stored, shared.SizeBytes)
	}
	trashResponse := f.api.Get("/api/v1/files/trash", f.cookie)
	requireFileStatus(t, trashResponse, http.StatusOK)
	var trash trashPage
	decodeFile(t, trashResponse, &trash)
	if len(trash.Items) != 1 || trash.Items[0].ID != first.PublicID || trash.Items[0].Name != "first.txt" {
		t.Fatalf("trash = %+v", trash)
	}
	if trash.Items[0].DeleteAfter.Sub(trash.Items[0].TrashedAt) != trashLifetime {
		t.Fatalf("delete after = %v, trashed at = %v", trash.Items[0].DeleteAfter, trash.Items[0].TrashedAt)
	}
	requireFileStatus(t, f.api.Post("/api/v1/files/"+first.PublicID+"/restore", f.cookie), http.StatusOK)
	requireFileStatus(t, f.api.Get("/api/v1/files/"+first.PublicID, f.cookie), http.StatusOK)
	requireFileStatus(t, f.api.Delete("/api/v1/files/"+first.PublicID, f.cookie), http.StatusNoContent)
	requireFileStatus(t, f.api.Delete("/api/v1/files/"+first.PublicID+"/permanent", f.cookie), http.StatusNoContent)
	if stored := f.storedBytes(t); stored != shared.SizeBytes {
		t.Fatalf("stored bytes after the first permanent deletion = %d, want %d", stored, shared.SizeBytes)
	}

	requireFileStatus(t, f.api.Delete("/api/v1/files/"+second.PublicID, f.cookie), http.StatusNoContent)
	requireFileStatus(t, f.api.Delete("/api/v1/files/"+second.PublicID+"/permanent", f.cookie), http.StatusNoContent)
	if stored := f.storedBytes(t); stored != 0 {
		t.Fatalf("stored bytes after the last deletion = %d", stored)
	}
	f.waitForObjectDeletion(t, sharedKey)
	for _, session := range []db.UploadSession{replacement, published} {
		row, err := f.queries.GetUploadSessionByPublicIDAndOwner(t.Context(), db.GetUploadSessionByPublicIDAndOwnerParams{
			PublicID: session.PublicID, OwnerID: f.ownerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if row.TargetNodeID.Valid || row.PublishedNodeID.Valid || row.PublishedVersionID.Valid || row.ExpectedRevision != session.ExpectedRevision {
			t.Fatalf("upload session = %+v", row)
		}
	}
}

func TestPurgeExpiredTrash(t *testing.T) {
	f := newFixture(t)
	const objectKey = "blobs/test/expired"
	node, _ := f.createFile(t, "expired.txt", f.storeBlob(t, objectKey, []byte("expired")))
	if err := f.queries.SetFileTrashedAt(t.Context(), db.SetFileTrashedAtParams{
		ID: node.ID, TrashedAt: pgtype.Timestamptz{Time: time.Now().Add(-31 * 24 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	worker := purgeTrashWorker{service: &Service{pool: f.pool, queries: f.queries, stores: f.stores, queue: f.queue}}
	if err := worker.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	response := f.api.Get("/api/v1/files/trash", f.cookie)
	requireFileStatus(t, response, http.StatusOK)
	var trash trashPage
	decodeFile(t, response, &trash)
	if len(trash.Items) != 0 {
		t.Fatalf("trash after purge = %+v", trash)
	}
	if stored := f.storedBytes(t); stored != 0 {
		t.Fatalf("stored bytes after purge = %d", stored)
	}
	f.waitForObjectDeletion(t, objectKey)
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

type fixture struct {
	api       humatest.TestAPI
	cookie    string
	pool      *pgxpool.Pool
	queries   *db.Queries
	stores    *storage.Service
	queue     *river.Client[pgx.Tx]
	backendID string
	ownerID   int64
	library   db.GetLibraryByPublicIDAndOwnerRow
}

func newFixture(t *testing.T) fixture {
	t.Helper()
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
	service := New(pool, stores, log)
	service.Register(protected)
	workers := river.NewWorkers()
	service.AddWorkers(workers)
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: log, Workers: workers, Queues: map[string]river.QueueConfig{"maintenance": {MaxWorkers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.UseQueue(queue)
	if err := queue.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := queue.Stop(stopCtx); err != nil {
			t.Errorf("stop River: %v", err)
		}
	})

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
	return fixture{
		api: api, cookie: cookie, pool: pool, queries: queries, stores: stores, queue: queue,
		backendID: backendID, ownerID: owner.ID, library: libraryRow,
	}
}

func (f fixture) storeBlob(t *testing.T, objectKey string, content []byte) db.Blob {
	t.Helper()
	checksum := sha256.Sum256(content)
	store, err := f.stores.ObjectStore(t.Context(), f.backendID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), objectKey, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := f.queries.CreateBlob(t.Context(), db.CreateBlobParams{
		PublicID: id.New(id.Blob), LibraryID: f.library.ID, SizeBytes: int64(len(content)),
		CiphertextSha256: checksum[:], DedupFingerprint: checksum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := f.queries.CreateBlobLocation(t.Context(), db.CreateBlobLocationParams{
		PublicID: id.New(id.BlobLocation), BlobID: blob.ID, LibraryID: f.library.ID,
		BackendID: f.library.BackendID, ObjectKey: objectKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.queries.MarkBlobLocationAvailable(t.Context(), location.ID); err != nil {
		t.Fatal(err)
	}
	return blob
}

func (f fixture) createFile(t *testing.T, name string, blob db.Blob) (db.Node, db.FileVersion) {
	t.Helper()
	node, err := f.queries.CreateUploadFileNode(t.Context(), db.CreateUploadFileNodeParams{
		PublicID: id.New(id.Node), LibraryID: f.library.ID,
		ParentID: pgtype.Int8{Int64: f.library.RootNodeID, Valid: true},
		Name:     pgtype.Text{String: name, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.queries.CreateFileVersion(t.Context(), db.CreateFileVersionParams{
		PublicID: id.New(id.FileVersion), NodeID: node.ID, LibraryID: f.library.ID,
		Ordinal: 1, BlobID: blob.ID, SizeBytes: blob.SizeBytes, ContentSha256: blob.CiphertextSha256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.queries.UpdateNodeCurrentVersion(t.Context(), db.UpdateNodeCurrentVersionParams{
		ID: node.ID, CurrentVersionID: pgtype.Int8{Int64: version.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	return node, version
}

func (f fixture) createUploadSession(t *testing.T, target pgtype.Int8) db.UploadSession {
	t.Helper()
	publicID := id.New(id.Upload)
	params := db.CreateUploadSessionParams{
		PublicID: publicID, OwnerID: f.ownerID, LibraryID: f.library.ID, ParentID: f.library.RootNodeID,
		TargetNodeID: target, Name: pgtype.Text{String: "upload.txt", Valid: true},
		StagingKey: publicID + ".part", DestinationKey: "objects/" + f.library.PublicID + "/" + publicID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
	if target.Valid {
		params.ExpectedRevision = pgtype.Int8{Int64: 2, Valid: true}
	}
	session, err := f.queries.CreateUploadSession(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (f fixture) storedBytes(t *testing.T) int64 {
	t.Helper()
	stored, err := f.queries.SumLibraryStoredBytes(t.Context(), f.library.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func (f fixture) waitForObjectDeletion(t *testing.T, key string) {
	t.Helper()
	store, err := f.stores.ObjectStore(t.Context(), f.backendID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := store.Stat(t.Context(), key)
		if errors.Is(err, storage.ErrMissing) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("object %s still exists", key)
		}
		time.Sleep(20 * time.Millisecond)
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
