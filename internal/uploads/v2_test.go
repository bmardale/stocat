package uploads

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryption"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/bmardale/stocat/internal/vault"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
)

var encode64 = base64.RawURLEncoding.EncodeToString

type v2Env struct {
	api        humatest.TestAPI
	queries    *db.Queries
	cookie     string
	userID     int64
	account    string
	key        ed25519.PrivateKey
	deployment []byte
	library    string
	root       string
}

type v2File struct {
	ciphertext []byte
	header     []byte
	contentID  []byte
}

func newV2Env(t *testing.T) v2Env {
	t.Helper()
	pool := testutil.NewPostgres(t)
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
	v1 := authService.Protected(api, "/api/v1")
	v2 := authService.Protected(api, "/api/v2")
	libraries.New(pool, log).Register(v1)
	vault.New(pool, log).Register(v2)
	stores := storage.New(pool, storage.Config{Logger: log})
	service, err := New(pool, nil, Config{StagingDir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := service.ConfigureQueue(stores)
	if err != nil {
		t.Fatal(err)
	}
	service.Register(v1)
	service.RegisterV2(v2)
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
		Config: []byte(`{"root":` + strconvQuote(t.TempDir()) + `}`), EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	email := strings.ToLower(id.New("test")) + "@example.com"
	register := api.Post("/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": "correct horse battery staple",
	})
	requireStatus(t, register, http.StatusCreated)
	result := register.Result()
	cookies := result.Cookies()
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	user, err := queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = queries.CreateUserEncryptionIdentity(t.Context(), db.CreateUserEncryptionIdentityParams{
		UserID: user.ID, Generation: 1, SigningPublicKey: public, RecipientPublicKey: make([]byte, 32),
		RecipientKeyID: make([]byte, 32), Certificate: []byte("identity"), CertificateSignature: make([]byte, 64),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = queries.CreateV2KeyBundle(t.Context(), db.CreateV2KeyBundleParams{
		UserID: user.ID, KdfSalt: make([]byte, 16), EncryptedMasterKey: []byte("password"),
		RecoveryEncryptedMasterKey: []byte("recovery"), PrivateKeyEnvelope: []byte("private"),
		IdentityGeneration: pgtype.Int8{Int64: 1, Valid: true}, BundleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	deployment, err := queries.GetEncryptionDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	env := v2Env{
		api: api, queries: queries, cookie: "Cookie: " + auth.CookieName + "=" + cookies[0].Value,
		userID: user.ID, account: user.PublicID, key: private, deployment: deployment.Bytes[:],
		library: id.New(id.Library), root: id.New(id.Node),
	}
	response := api.Post("/api/v2/libraries", env.cookie, map[string]any{
		"backend_id": backendID, "root_metadata": env.metadata(t, env.root),
		"owner_root_envelope": encode64(env.record(t, "owner-root", map[string]string{
			"account_id": env.account, "library_id": env.library, "node_id": env.root, "generation": "1",
			"node_epoch": "1", "revision": "1", "nonce": encode64(randomV2Bytes(t, 24)), "ciphertext": encode64(randomV2Bytes(t, 48)),
		})),
	})
	requireStatus(t, response, http.StatusCreated)
	return env
}

func randomV2Bytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (e v2Env) record(t *testing.T, kind string, fields map[string]string) []byte {
	t.Helper()
	data, err := encryptionv2.Record{Type: kind, DeploymentID: encode64(e.deployment), Fields: fields}.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (e v2Env) sign(t *testing.T, data []byte) encryption.SignedRecord {
	t.Helper()
	input, err := encryptionv2.SignatureInput(data)
	if err != nil {
		t.Fatal(err)
	}
	return encryption.Encode(data, ed25519.Sign(e.key, input))
}

func (e v2Env) metadata(t *testing.T, node string) encryption.SignedRecord {
	t.Helper()
	return e.sign(t, e.record(t, "node-metadata", map[string]string{
		"account_id": e.account, "library_id": e.library, "node_id": node, "node_epoch": "1", "revision": "1",
		"nonce": encode64(randomV2Bytes(t, 24)), "ciphertext": encode64(randomV2Bytes(t, 64)),
	}))
}

func (e v2Env) newFile(t *testing.T, node string, size int, parentEpoch int) map[string]any {
	t.Helper()
	return map[string]any{
		"library_id": e.library, "size": size, "metadata": e.metadata(t, node),
		"name_token": encode64(randomV2Bytes(t, 32)),
		"parent_envelope": e.sign(t, e.record(t, "parent-envelope", map[string]string{
			"account_id": e.account, "library_id": e.library, "parent_id": e.root, "child_id": node,
			"parent_epoch": strconv.Itoa(parentEpoch), "child_epoch": "1", "generation": "1", "revision": "1",
			"nonce": encode64(randomV2Bytes(t, 24)), "ciphertext": encode64(randomV2Bytes(t, 48)),
		})),
	}
}

// newV2File returns synthetic ciphertext. The server checks the header and hash, but it cannot check frames.
func newV2File(t *testing.T, plaintextSize uint64) v2File {
	t.Helper()
	header := encryptionv2.FileHeader{FrameSize: encryptionv2.MinFrameSize, PlaintextSize: plaintextSize}
	copy(header.ContentID[:], randomV2Bytes(t, 16))
	copy(header.NoncePrefix[:], randomV2Bytes(t, 16))
	encoded, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	size, err := header.CiphertextSize()
	if err != nil {
		t.Fatal(err)
	}
	return v2File{
		ciphertext: append(encoded, randomV2Bytes(t, int(size)-encryptionv2.HeaderSize)...),
		header:     encoded, contentID: header.ContentID[:],
	}
}

func (e v2Env) completion(t *testing.T, node, version string, file v2File, revision int) map[string]any {
	t.Helper()
	fileKey := e.record(t, "file-key", map[string]string{
		"account_id": e.account, "library_id": e.library, "node_id": node, "version_id": version,
		"content_id": encode64(file.contentID), "node_epoch": "1", "generation": "1",
		"nonce": encode64(randomV2Bytes(t, 24)), "ciphertext": encode64(randomV2Bytes(t, 48)),
	})
	keyHash, ciphertextHash := sha256.Sum256(fileKey), sha256.Sum256(file.ciphertext)
	manifest := e.record(t, "version-manifest", map[string]string{
		"account_id": e.account, "library_id": e.library, "node_id": node, "version_id": version,
		"content_id": encode64(file.contentID), "node_epoch": "1", "revision": strconv.Itoa(revision),
		"header": encode64(file.header), "ciphertext_hash": encode64(ciphertextHash[:]), "key_envelope_hash": encode64(keyHash[:]),
	})
	current := e.record(t, "current-version", map[string]string{
		"account_id": e.account, "library_id": e.library, "node_id": node, "node_epoch": "1",
		"revision": strconv.Itoa(revision), "version_id": version,
	})
	return map[string]any{"file_key": encode64(fileKey), "manifest": e.sign(t, manifest), "current_version": e.sign(t, current)}
}

func (e v2Env) start(t *testing.T, body map[string]any, content []byte) Upload {
	t.Helper()
	response := e.api.Post("/api/v2/uploads", e.cookie, body)
	requireStatus(t, response, http.StatusCreated)
	var upload Upload
	decodeBody(t, response.Body.Bytes(), &upload)
	response = e.api.Do(http.MethodPatch, upload.UploadURL, e.cookie, "Tus-Resumable: 1.0.0",
		"Content-Type: application/offset+octet-stream", "Content-Length: "+strconv.Itoa(len(content)),
		"Upload-Offset: 0", bytes.NewReader(content))
	requireStatus(t, response, http.StatusNoContent)
	return upload
}

func (e v2Env) fileNode(t *testing.T) vault.EncryptedNode {
	t.Helper()
	response := e.api.Get("/api/v2/libraries/"+e.library+"/nodes", e.cookie)
	requireStatus(t, response, http.StatusOK)
	var page struct {
		Items []vault.EncryptedNode `json:"items"`
	}
	decodeBody(t, response.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Kind != "file" || page.Items[0].CurrentVersion == nil {
		t.Fatalf("encrypted nodes = %+v", page.Items)
	}
	return page.Items[0]
}

func TestEncryptedUploadsPublishFiles(t *testing.T) {
	env := newV2Env(t)
	file := newV2File(t, 6)
	nodeID := id.New(id.Node)
	create := env.newFile(t, nodeID, len(file.ciphertext), 1)

	for name, change := range map[string]func(map[string]any){
		"parent epoch 2":          func(body map[string]any) { maps.Copy(body, env.newFile(t, nodeID, len(file.ciphertext), 2)) },
		"records with a target":   func(body map[string]any) { body["target_node_id"] = nodeID },
		"size below the minimum":  func(body map[string]any) { body["size"] = 79 },
		"node records missing":    func(body map[string]any) { delete(body, "parent_envelope") },
		"revision for a new file": func(body map[string]any) { body["expected_revision"] = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			body := maps.Clone(create)
			change(body)
			requireStatus(t, env.api.Post("/api/v2/uploads", env.cookie, body), http.StatusUnprocessableEntity)
		})
	}
	requireStatus(t, env.api.Post("/api/v1/uploads", env.cookie, map[string]any{
		"library_id": env.library, "name": "plain.txt", "size": 5,
	}), http.StatusNotFound)

	upload := env.start(t, create, file.ciphertext)
	requireStatus(t, env.api.Post("/api/v1/uploads/"+upload.ID+"/complete", env.cookie, map[string]any{}), http.StatusNotFound)
	versionID := id.New(id.FileVersion)
	completeURL := "/api/v2/uploads/" + upload.ID + "/complete"
	requireStatus(t, env.api.Post(completeURL, env.cookie, env.completion(t, nodeID, versionID, file, 3)), http.StatusUnprocessableEntity)
	substituted := env.completion(t, nodeID, versionID, file, 2)
	substituted["file_key"] = env.completion(t, nodeID, versionID, file, 2)["file_key"]
	requireStatus(t, env.api.Post(completeURL, env.cookie, substituted), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.Post(completeURL, env.cookie, env.completion(t, nodeID, versionID, file, 2)), http.StatusOK)
	published := waitForUpload(t, env.queries, env.userID, upload.ID)
	version, err := env.queries.GetFileVersionByID(t.Context(), published.PublishedVersionID.Int64)
	if err != nil || version.SizeBytes != 6 || version.PublicID != versionID || version.ContentSha256 != nil {
		t.Fatalf("encrypted version = %+v, %v", version, err)
	}
	if node := env.fileNode(t); node.ID != nodeID || node.Revision != "2" {
		t.Fatalf("published node = %+v", node)
	}

	replacement := newV2File(t, encryptionv2.MinFrameSize+1)
	upload = env.start(t, map[string]any{
		"library_id": env.library, "target_node_id": nodeID, "expected_revision": "2", "size": len(replacement.ciphertext),
	}, replacement.ciphertext)
	requireStatus(t, env.api.Post("/api/v2/uploads/"+upload.ID+"/complete", env.cookie,
		env.completion(t, nodeID, id.New(id.FileVersion), replacement, 3)), http.StatusOK)
	waitForUpload(t, env.queries, env.userID, upload.ID)
	if node := env.fileNode(t); node.Revision != "3" {
		t.Fatalf("replaced node = %+v", node)
	}

	stale := newV2File(t, 1)
	upload = env.start(t, map[string]any{
		"library_id": env.library, "target_node_id": nodeID, "expected_revision": "2", "size": len(stale.ciphertext),
	}, stale.ciphertext)
	requireStatus(t, env.api.Post("/api/v2/uploads/"+upload.ID+"/complete", env.cookie,
		env.completion(t, nodeID, id.New(id.FileVersion), stale, 3)), http.StatusOK)
	waitForUploadState(t, env.queries, env.userID, upload.ID, "conflict")

	tampered := newV2File(t, 1)
	tamperedNode := id.New(id.Node)
	sent := bytes.Clone(tampered.ciphertext)
	sent[len(sent)-1] ^= 1
	upload = env.start(t, env.newFile(t, tamperedNode, len(sent), 1), sent)
	requireStatus(t, env.api.Post("/api/v2/uploads/"+upload.ID+"/complete", env.cookie,
		env.completion(t, tamperedNode, id.New(id.FileVersion), tampered, 2)), http.StatusOK)
	if failed := waitForUploadState(t, env.queries, env.userID, upload.ID, "failed"); failed.FailureCode.String != "invalid_encryption" {
		t.Fatalf("tampered upload failure = %q", failed.FailureCode.String)
	}
}
