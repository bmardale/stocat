package trash

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
	"github.com/bmardale/stocat/internal/files"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func TestTrashFileLifecycle(t *testing.T) {
	f := newFixture(t)
	const objectKey = "blobs/test/notes"
	file, _ := f.createFile(t, "notes.txt", f.library.RootNodeID, f.storeBlob(t, objectKey, []byte("hello")))

	page := decodeTrash[trashPage](t, f.api.Get("/api/v1/trash", f.cookie))
	if len(page.Items) != 0 {
		t.Fatalf("initial trash = %+v", page.Items)
	}
	requireTrashStatus(t, f.api.Delete("/api/v1/files/"+file.PublicID), http.StatusUnauthorized)
	requireTrashStatus(t, f.api.Delete("/api/v1/files/"+file.PublicID, f.cookie), http.StatusNoContent)
	requireTrashStatus(t, f.api.Delete("/api/v1/files/"+file.PublicID, f.cookie), http.StatusNotFound)
	requireTrashStatus(t, f.api.Get("/api/v1/files/"+file.PublicID, f.cookie), http.StatusNotFound)

	page = decodeTrash[trashPage](t, f.api.Get("/api/v1/trash", f.cookie))
	if len(page.Items) != 1 {
		t.Fatalf("trash items = %+v", page.Items)
	}
	item := page.Items[0]
	if item.ID != file.PublicID || item.Kind != "file" || item.Name != "notes.txt" || item.LibraryName != "Documents" {
		t.Fatalf("trash item = %+v", item)
	}
	if want := item.TrashedAt.Add(Retention); !item.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %s, want %s", item.ExpiresAt, want)
	}

	requireTrashStatus(t, f.api.Post("/api/v1/trash/"+file.PublicID+"/restore", f.cookie), http.StatusNoContent)
	requireTrashStatus(t, f.api.Get("/api/v1/files/"+file.PublicID, f.cookie), http.StatusOK)

	requireTrashStatus(t, f.api.Delete("/api/v1/files/"+file.PublicID, f.cookie), http.StatusNoContent)
	requireTrashStatus(t, f.api.Delete("/api/v1/trash/"+file.PublicID, f.cookie), http.StatusNoContent)
	requireTrashStatus(t, f.api.Post("/api/v1/trash/"+file.PublicID+"/restore", f.cookie), http.StatusNotFound)
	if stored := f.storedBytes(t); stored != 0 {
		t.Fatalf("stored bytes after purge = %d", stored)
	}
	f.waitForObjectDeletion(t, objectKey)
}

func TestTrashFolderCascadesAndRestores(t *testing.T) {
	f := newFixture(t)
	folder := f.createFolderNode(t, "Archive", pgtype.Int8{Int64: f.library.RootNodeID, Valid: true})
	f.createFile(t, "inside.txt", folder.ID, f.storeBlob(t, "blobs/test/inside", []byte("inside")))

	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusNoContent)
	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusNotFound)
	requireTrashStatus(t, f.api.Get("/api/v1/libraries/"+f.library.PublicID+"/nodes", f.cookie), http.StatusOK)

	page := decodeTrash[trashPage](t, f.api.Get("/api/v1/trash", f.cookie))
	if len(page.Items) != 1 || page.Items[0].ID != folder.PublicID || page.Items[0].Kind != "folder" {
		t.Fatalf("trash items = %+v", page.Items)
	}

	requireTrashStatus(t, f.api.Post("/api/v1/trash/"+folder.PublicID+"/restore", f.cookie), http.StatusNoContent)
	nodes := decodeTrash[nodesListResponse](t, f.api.Get("/api/v1/libraries/"+f.library.PublicID+"/nodes", f.cookie))
	if len(nodes.Items) != 1 || nodes.Items[0].ID != folder.PublicID {
		t.Fatalf("restored nodes = %+v", nodes.Items)
	}

	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusNoContent)
	f.createFolderNode(t, "Archive", pgtype.Int8{Int64: f.library.RootNodeID, Valid: true})
	requireTrashStatus(t, f.api.Post("/api/v1/trash/"+folder.PublicID+"/restore", f.cookie), http.StatusConflict)
}

func TestTrashFolderWaitsForActiveUpload(t *testing.T) {
	f := newFixture(t)
	folder := f.createFolderNode(t, "Active", pgtype.Int8{Int64: f.library.RootNodeID, Valid: true})
	session := f.createUploadSession(t, folder.ID)

	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusConflict)
	f.cancelUploadSession(t, session)
	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusNoContent)
}

func TestPurgeWaitsForActiveUpload(t *testing.T) {
	f := newFixture(t)
	folder := f.createFolderNode(t, "Active", pgtype.Int8{Int64: f.library.RootNodeID, Valid: true})
	requireTrashStatus(t, f.api.Delete("/api/v1/libraries/"+f.library.PublicID+"/folders/"+folder.PublicID, f.cookie), http.StatusNoContent)
	session := f.createUploadSession(t, folder.ID)

	requireTrashStatus(t, f.api.Delete("/api/v1/trash/"+folder.PublicID, f.cookie), http.StatusConflict)
	f.cancelUploadSession(t, session)
	requireTrashStatus(t, f.api.Delete("/api/v1/trash/"+folder.PublicID, f.cookie), http.StatusNoContent)
}

func TestExpiredTrashRoots(t *testing.T) {
	f := newFixture(t)
	file, _ := f.createFile(t, "old.txt", f.library.RootNodeID, f.storeBlob(t, "blobs/test/old", []byte("old")))
	requireTrashStatus(t, f.api.Delete("/api/v1/files/"+file.PublicID, f.cookie), http.StatusNoContent)

	roots, err := f.queries.ListExpiredTrashRoots(t.Context(), db.ListExpiredTrashRootsParams{
		Cutoff: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}, PageLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].PublicID != file.PublicID {
		t.Fatalf("expired roots = %+v", roots)
	}
	future, err := f.queries.ListExpiredTrashRoots(t.Context(), db.ListExpiredTrashRootsParams{
		Cutoff: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}, PageLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Fatalf("future roots = %+v", future)
	}
}

type nodesListResponse struct {
	Items []libraries.Node `json:"items"`
}

type fixture struct {
	api       humatest.TestAPI
	cookie    string
	queries   *db.Queries
	stores    *storage.Service
	backendID string
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
	queries := db.New(pool)
	backendID := id.New(id.StorageBackend)
	if _, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: backendID, Name: "Local", Type: storage.TypeLocal,
		Config: []byte(`{"root":` + quote(root) + `}`), EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	stores := storage.New(pool, storage.Config{Logger: log})
	fileService := files.New(pool, stores, log)
	fileService.Register(protected)
	service := New(pool, log)
	service.Register(protected)
	workers := river.NewWorkers()
	fileService.AddWorkers(workers)
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

	cookie, email := register(t, api)
	owner, err := queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	libraryResponse := api.Post("/api/v1/libraries", cookie, map[string]any{
		"name": "Documents", "backend_id": backendID, "encryption_mode": libraries.EncryptionNone,
	})
	requireTrashStatus(t, libraryResponse, http.StatusCreated)
	var library libraries.Library
	decodeTrashRaw(t, libraryResponse, &library)
	libraryRow, err := queries.GetLibraryByPublicIDAndOwner(t.Context(), db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: library.ID, OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		api: api, cookie: cookie, queries: queries, stores: stores,
		backendID: backendID, library: libraryRow,
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

func (f fixture) createFile(t *testing.T, name string, parentID int64, blob db.Blob) (db.Node, db.FileVersion) {
	t.Helper()
	node, err := f.queries.CreateUploadFileNode(t.Context(), db.CreateUploadFileNodeParams{
		PublicID: id.New(id.Node), LibraryID: f.library.ID,
		ParentID: pgtype.Int8{Int64: parentID, Valid: true}, Name: pgtype.Text{String: name, Valid: true},
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

func (f fixture) createFolderNode(t *testing.T, name string, parent pgtype.Int8) db.Node {
	t.Helper()
	node, err := f.queries.CreateNode(t.Context(), db.CreateNodeParams{
		PublicID: id.New(id.Node), LibraryID: f.library.ID, ParentID: parent,
		Kind: "folder", Name: pgtype.Text{String: name, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func (f fixture) createUploadSession(t *testing.T, parentID int64) db.UploadSession {
	t.Helper()
	session, err := f.queries.CreateUploadSession(t.Context(), db.CreateUploadSessionParams{
		PublicID: id.New(id.Upload), OwnerID: f.library.OwnerID, LibraryID: f.library.ID,
		ParentID: parentID, Name: pgtype.Text{String: "new.txt", Valid: true}, DeclaredSize: 10,
		StagingKey: id.New(id.Upload) + ".part", DestinationKey: "objects/" + id.New(id.Upload),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (f fixture) cancelUploadSession(t *testing.T, session db.UploadSession) {
	t.Helper()
	if _, err := f.queries.CancelUploadSession(t.Context(), db.CancelUploadSessionParams{
		PublicID: session.PublicID, OwnerID: f.library.OwnerID,
	}); err != nil {
		t.Fatal(err)
	}
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
	requireTrashStatus(t, response, http.StatusCreated)
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

func requireTrashStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func decodeTrashRaw(t *testing.T, response *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), value); err != nil {
		t.Fatal(err)
	}
}

func decodeTrash[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	decodeTrashRaw(t, response, &value)
	return value
}
