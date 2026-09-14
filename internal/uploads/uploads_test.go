package uploads

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
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
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTusUploadsPublishFiles(t *testing.T) {
	pool := testutil.NewPostgres(t)
	stagingDir := t.TempDir()
	objectDir := t.TempDir()
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
	service, err := New(pool, nil, Config{StagingDir: stagingDir, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := service.ConfigureQueue(stores)
	if err != nil {
		t.Fatal(err)
	}
	service.Register(protected)
	if err := queue.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := queue.Stop(stopCtx); err != nil {
			t.Errorf("stop River: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Errorf("close uploads: %v", err)
		}
	})

	queries := db.New(pool)
	backendID := id.New(id.StorageBackend)
	if _, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: backendID, Name: "Local", Type: storage.TypeLocal,
		Config: []byte(`{"root":` + strconvQuote(objectDir) + `}`), EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	register := api.Post("/api/v1/auth/register", map[string]string{
		"name": "Test", "email": strings.ToLower(id.New("test")) + "@example.com", "password": "correct horse battery staple",
	})
	requireStatus(t, register, http.StatusCreated)
	registerResult := register.Result()
	cookies := registerResult.Cookies()
	if err := registerResult.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cookie := "Cookie: " + auth.CookieName + "=" + cookies[0].Value
	createdLibrary := api.Post("/api/v1/libraries", cookie, map[string]any{
		"name": "Documents", "backend_id": backendID, "encryption_mode": EncryptionNone,
	})
	requireStatus(t, createdLibrary, http.StatusCreated)
	var library struct {
		ID string `json:"id"`
	}
	decodeBody(t, createdLibrary.Body.Bytes(), &library)

	content := "resumable upload content"
	createdUpload := api.Post("/api/v1/uploads", cookie, map[string]any{
		"library_id": library.ID, "name": "notes.txt", "size": len(content),
	})
	requireStatus(t, createdUpload, http.StatusCreated)
	var upload Upload
	decodeBody(t, createdUpload.Body.Bytes(), &upload)
	requireStatus(t, api.Do(http.MethodHead, upload.UploadURL, "Tus-Resumable: 1.0.0"), http.StatusUnauthorized)
	head := api.Do(http.MethodHead, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0")
	requireStatus(t, head, http.StatusNoContent)
	if head.Header().Get("Upload-Offset") != "0" || head.Header().Get("Upload-Length") != "24" {
		t.Fatalf("tus head offset = %q, length = %q", head.Header().Get("Upload-Offset"), head.Header().Get("Upload-Length"))
	}
	wrongOffset := api.Do(http.MethodPatch, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: 1", "Upload-Offset: 2", strings.NewReader("x"))
	requireStatus(t, wrongOffset, http.StatusConflict)

	first := content[:10]
	response := api.Do(http.MethodPatch, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: 10", "Upload-Offset: 0", strings.NewReader(first))
	requireStatus(t, response, http.StatusNoContent)
	if response.Header().Get("Upload-Offset") != "10" {
		t.Fatalf("first offset = %q", response.Header().Get("Upload-Offset"))
	}
	response = api.Do(http.MethodPatch, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: 14", "Upload-Offset: 10", strings.NewReader(content[10:]))
	requireStatus(t, response, http.StatusNoContent)

	user, err := queries.GetUserByEmail(t.Context(), jsonEmail(register.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	waitForUpload(t, queries, user.ID, upload.ID)
	libraryRow, err := queries.GetLibraryByPublicIDAndOwner(t.Context(), db.GetLibraryByPublicIDAndOwnerParams{PublicID: library.ID, OwnerID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := queries.ListChildNodes(t.Context(), db.ListChildNodesParams{
		LibraryID: libraryRow.ID, ParentID: validInt(libraryRow.RootNodeID), AfterID: 0, PageLimit: 10,
	})
	if err != nil || len(rows) != 1 || rows[0].Name.String != "notes.txt" || !rows[0].CurrentVersionID.Valid {
		t.Fatalf("published nodes = %+v, %v", rows, err)
	}
	staleUpload := api.Post("/api/v1/uploads", cookie, map[string]any{
		"library_id": library.ID, "target_node_id": rows[0].PublicID, "expected_revision": 1,
		"size": len("replacement"),
	})
	requireStatus(t, staleUpload, http.StatusCreated)
	decodeBody(t, staleUpload.Body.Bytes(), &upload)
	response = api.Do(http.MethodPatch, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: 11", "Upload-Offset: 0",
		strings.NewReader("replacement"))
	requireStatus(t, response, http.StatusNoContent)
	waitForUploadState(t, queries, user.ID, upload.ID, "conflict")

	encryptedLibrary := api.Post("/api/v1/libraries", cookie, map[string]any{
		"name": "Private", "backend_id": backendID, "encryption_mode": EncryptionE2EE, "key_envelope": []byte("wrapped-library-key"),
	})
	requireStatus(t, encryptedLibrary, http.StatusCreated)
	decodeBody(t, encryptedLibrary.Body.Bytes(), &library)
	header := make([]byte, frameHeaderSize)
	copy(header, frameMagic[:])
	header[8] = 1
	binary.BigEndian.PutUint32(header[12:16], minFrameSize)
	binary.BigEndian.PutUint64(header[16:24], 6)
	ciphertext := append(header, make([]byte, 6+frameTagSize)...)
	createdUpload = api.Post("/api/v1/uploads", cookie, map[string]any{
		"library_id": library.ID, "encrypted_name": []byte("encrypted-name"),
		"name_token": make([]byte, 32), "size": len(ciphertext),
	})
	requireStatus(t, createdUpload, http.StatusCreated)
	decodeBody(t, createdUpload.Body.Bytes(), &upload)
	response = api.Do(http.MethodPatch, upload.UploadURL, cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: "+fmt.Sprint(len(ciphertext)),
		"Upload-Offset: 0", bytes.NewReader(ciphertext))
	requireStatus(t, response, http.StatusNoContent)
	response = api.Post("/api/v1/uploads/"+upload.ID+"/complete", cookie, map[string]any{
		"dedup_fingerprint": make([]byte, 32), "encryption_format": FormatE2EEV1,
		"encrypted_file_key": []byte("wrapped-file-key"),
	})
	requireStatus(t, response, http.StatusOK)
	waitForUpload(t, queries, user.ID, upload.ID)
	row, err := queries.GetUploadSessionByPublicIDAndOwner(t.Context(), db.GetUploadSessionByPublicIDAndOwnerParams{
		PublicID: upload.ID, OwnerID: user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := queries.GetFileVersionByID(t.Context(), row.PublishedVersionID.Int64)
	if err != nil || version.SizeBytes != 6 {
		t.Fatalf("encrypted version = %+v, %v", version, err)
	}

	cancelLibrary := api.Post("/api/v1/libraries", cookie, map[string]any{
		"name": "Cancel", "backend_id": backendID, "encryption_mode": EncryptionNone,
	})
	requireStatus(t, cancelLibrary, http.StatusCreated)
	var cancelTarget struct {
		ID string `json:"id"`
	}
	decodeBody(t, cancelLibrary.Body.Bytes(), &cancelTarget)
	createdUpload = api.Post("/api/v1/uploads", cookie, map[string]any{
		"library_id": cancelTarget.ID, "name": "cancel.txt", "size": len("cancel me"),
	})
	requireStatus(t, createdUpload, http.StatusCreated)
	decodeBody(t, createdUpload.Body.Bytes(), &upload)
	response = api.Do(http.MethodDelete, "/api/v1/uploads/"+upload.ID, cookie)
	requireStatus(t, response, http.StatusNoContent)

	if _, err := queries.CreateUserBackendQuota(t.Context(), db.CreateUserBackendQuotaParams{
		UserID: user.ID, BackendPublicID: backendID, LimitBytes: pgtype.Int8{Int64: 64, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	overQuota := api.Post("/api/v1/uploads", cookie, map[string]any{
		"library_id": cancelTarget.ID, "name": "large.txt", "size": 64,
	})
	requireStatus(t, overQuota, http.StatusRequestEntityTooLarge)
	if !strings.Contains(overQuota.Body.String(), "storage quota on Local") {
		t.Fatalf("quota error = %s", overQuota.Body)
	}
}

func waitForUpload(t *testing.T, queries *db.Queries, ownerID int64, uploadID string) db.UploadSession {
	return waitForUploadState(t, queries, ownerID, uploadID, stateCompleted)
}

func waitForUploadState(t *testing.T, queries *db.Queries, ownerID int64, uploadID, state string) db.UploadSession {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		upload, err := queries.GetUploadSessionByPublicIDAndOwner(t.Context(), db.GetUploadSessionByPublicIDAndOwnerParams{
			PublicID: uploadID, OwnerID: ownerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if upload.State == state {
			return upload
		}
		if upload.State == "failed" || upload.State == "conflict" {
			t.Fatalf("upload state = %q: %s", upload.State, upload.FailureMessage.String)
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload state = %q", upload.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got := response.Code; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func decodeBody(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func jsonEmail(data []byte) string {
	var user struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(data, &user)
	return user.Email
}
