package vault

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
)

var b64 = base64.RawURLEncoding.EncodeToString

type testEnv struct {
	api        humatest.TestAPI
	queries    *db.Queries
	deployment []byte
	backendID  string
}

type testUser struct {
	id     int64
	public string
	header string
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	pool := testutil.NewPostgres(t)
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
	libraries.New(pool, log).Register(authService.Protected(api, "/api/v1"))
	New(pool, log).Register(authService.Protected(api, "/api/v2"))
	queries := db.New(pool)
	deployment, err := queries.GetEncryptionDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	backendID := id.New(id.StorageBackend)
	if _, err := queries.CreateStorageBackend(t.Context(), db.CreateStorageBackendParams{
		PublicID: backendID, Name: "Local", Type: "local", Config: []byte(`{"root":"/tmp/stocat-vault-test"}`),
		EncryptedSecrets: "test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return testEnv{api: api, queries: queries, deployment: deployment.Bytes[:], backendID: backendID}
}

func (e testEnv) signUp(t *testing.T) testUser {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": "correct horse battery staple",
	})
	requireStatus(t, response, http.StatusCreated)
	result := response.Result()
	defer func() { _ = result.Body.Close() }()
	user, err := e.queries.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	return testUser{id: user.ID, public: user.PublicID, header: "Cookie: " + auth.CookieName + "=" + result.Cookies()[0].Value}
}

// configure stores an identity directly. The service reads only its signing public key.
func (e testEnv) configure(t *testing.T, user testUser) ed25519.PrivateKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.queries.CreateUserEncryptionIdentity(t.Context(), db.CreateUserEncryptionIdentityParams{
		UserID: user.id, Generation: 1, SigningPublicKey: public, RecipientPublicKey: make([]byte, 32),
		RecipientKeyID: make([]byte, 32), Certificate: []byte("identity"), CertificateSignature: make([]byte, 64),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.queries.CreateV2KeyBundle(t.Context(), db.CreateV2KeyBundleParams{
		UserID: user.id, KdfSalt: make([]byte, 16), EncryptedMasterKey: []byte("password"),
		RecoveryEncryptedMasterKey: []byte("recovery"), PrivateKeyEnvelope: []byte("private"),
		IdentityGeneration: pgtype.Int8{Int64: 1, Valid: true}, BundleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return private
}

func random(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return b64(value)
}

func (e testEnv) record(t *testing.T, kind string, fields map[string]string) []byte {
	t.Helper()
	data, err := encryptionv2.Record{Type: kind, DeploymentID: b64(e.deployment), Fields: fields}.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sign(t *testing.T, key ed25519.PrivateKey, data []byte) encryption.SignedRecord {
	t.Helper()
	input, err := encryptionv2.SignatureInput(data)
	if err != nil {
		t.Fatal(err)
	}
	return encryption.Encode(data, ed25519.Sign(key, input))
}

func (e testEnv) metadata(t *testing.T, key ed25519.PrivateKey, account, library, node string, epoch, revision int) encryption.SignedRecord {
	t.Helper()
	return sign(t, key, e.record(t, "node-metadata", map[string]string{
		"account_id": account, "library_id": library, "node_id": node, "node_epoch": strconv.Itoa(epoch),
		"revision": strconv.Itoa(revision), "nonce": random(t, 24), "ciphertext": random(t, 64),
	}))
}

func (e testEnv) envelope(t *testing.T, key ed25519.PrivateKey, account, library, parent, child string, parentEpoch int) encryption.SignedRecord {
	t.Helper()
	return sign(t, key, e.record(t, "parent-envelope", map[string]string{
		"account_id": account, "library_id": library, "parent_id": parent, "child_id": child,
		"parent_epoch": strconv.Itoa(parentEpoch), "child_epoch": "1", "generation": "1", "revision": "1",
		"nonce": random(t, 24), "ciphertext": random(t, 48),
	}))
}

func (e testEnv) ownerRoot(t *testing.T, account, library, node string, generation int) string {
	t.Helper()
	return b64(e.record(t, "owner-root", map[string]string{
		"account_id": account, "library_id": library, "node_id": node, "generation": strconv.Itoa(generation),
		"node_epoch": "1", "revision": "1", "nonce": random(t, 24), "ciphertext": random(t, 48),
	}))
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

func TestEncryptedLibraries(t *testing.T) {
	env := newTestEnv(t)
	owner, other := env.signUp(t), env.signUp(t)
	ctx := t.Context()
	libraryID, rootID := id.New(id.Library), id.New(id.Node)
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	creation := func(key ed25519.PrivateKey) map[string]any {
		return map[string]any{
			"backend_id":          env.backendID,
			"root_metadata":       env.metadata(t, key, owner.public, libraryID, rootID, 1, 1),
			"owner_root_envelope": env.ownerRoot(t, owner.public, libraryID, rootID, 1),
		}
	}
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/libraries", owner.header, creation(stranger)), http.StatusConflict)
	key := env.configure(t, owner)

	t.Run("rejects invalid library records", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(body map[string]any)
		}{
			{"metadata signed by another key", func(body map[string]any) {
				body["root_metadata"] = env.metadata(t, stranger, owner.public, libraryID, rootID, 1, 1)
			}},
			{"metadata revision 2", func(body map[string]any) {
				body["root_metadata"] = env.metadata(t, key, owner.public, libraryID, rootID, 1, 2)
			}},
			{"metadata of another account", func(body map[string]any) {
				body["root_metadata"] = env.metadata(t, key, other.public, libraryID, rootID, 1, 1)
			}},
			{"owner root of another generation", func(body map[string]any) {
				body["owner_root_envelope"] = env.ownerRoot(t, owner.public, libraryID, rootID, 2)
			}},
			{"owner root of another node", func(body map[string]any) {
				body["owner_root_envelope"] = env.ownerRoot(t, owner.public, libraryID, id.New(id.Node), 1)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := creation(key)
				tc.change(body)
				requireStatus(t, env.api.PostCtx(ctx, "/api/v2/libraries", owner.header, body), http.StatusUnprocessableEntity)
			})
		}
	})

	response := env.api.PostCtx(ctx, "/api/v2/libraries", owner.header, creation(key))
	requireStatus(t, response, http.StatusCreated)
	library := decode[EncryptedLibrary](t, response)
	if library.ID != libraryID || library.RootNodeID != rootID || library.MetadataRevision != "1" || library.RootKeyEpoch != "1" {
		t.Fatalf("library = %+v", library)
	}
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/libraries", owner.header, creation(key)), http.StatusConflict)
	if listed := decode[[]EncryptedLibrary](t, env.api.GetCtx(ctx, "/api/v2/libraries", owner.header)); len(listed) != 1 {
		t.Fatalf("encrypted libraries = %+v", listed)
	}
	if legacy := decode[[]libraries.Library](t, env.api.GetCtx(ctx, "/api/v1/libraries", owner.header)); len(legacy) != 0 {
		t.Fatalf("v1 libraries include the v2 library: %+v", legacy)
	}
	requireStatus(t, env.api.GetCtx(ctx, "/api/v1/libraries/"+libraryID, owner.header), http.StatusNotFound)
	requireStatus(t, env.api.GetCtx(ctx, "/api/v2/libraries/"+libraryID, other.header), http.StatusNotFound)

	folderID := id.New(id.Node)
	folderToken := random(t, 32)
	folderBody := func(parent, child string, parentEpoch int, token string) map[string]any {
		return map[string]any{
			"metadata":        env.metadata(t, key, owner.public, libraryID, child, 1, 1),
			"parent_envelope": env.envelope(t, key, owner.public, libraryID, parent, child, parentEpoch),
			"name_token":      token,
		}
	}
	foldersURL := "/api/v2/libraries/" + libraryID + "/folders"
	mismatched := folderBody(rootID, folderID, 1, folderToken)
	mismatched["parent_envelope"] = env.envelope(t, key, owner.public, libraryID, rootID, id.New(id.Node), 1)
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, mismatched), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(rootID, folderID, 2, folderToken)), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(id.New(id.Node), folderID, 1, folderToken)), http.StatusNotFound)
	response = env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(rootID, folderID, 1, folderToken))
	requireStatus(t, response, http.StatusCreated)
	folder := decode[EncryptedNode](t, response)
	if folder.ID != folderID || folder.ParentID != rootID || folder.Kind != kindFolder || folder.NameToken != folderToken {
		t.Fatalf("folder = %+v", folder)
	}
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(rootID, id.New(id.Node), 1, folderToken)), http.StatusConflict)
	siblingToken := random(t, 32)
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(rootID, id.New(id.Node), 1, siblingToken)), http.StatusCreated)
	requireStatus(t, env.api.PostCtx(ctx, foldersURL, owner.header, folderBody(folderID, id.New(id.Node), 1, random(t, 32))), http.StatusCreated)

	rootPage := decode[encryptedNodesPage](t, env.api.GetCtx(ctx, "/api/v2/libraries/"+libraryID+"/nodes", owner.header))
	if len(rootPage.Items) != 2 || rootPage.Items[0].ParentEnvelope != folder.ParentEnvelope || rootPage.Items[0].Metadata != folder.Metadata {
		t.Fatalf("root page = %+v", rootPage)
	}
	firstPage := decode[encryptedNodesPage](t, env.api.GetCtx(ctx, "/api/v2/libraries/"+libraryID+"/nodes?limit=1", owner.header))
	if len(firstPage.Items) != 1 || firstPage.NextCursor != folderID {
		t.Fatalf("first page = %+v", firstPage)
	}
	if nested := decode[encryptedNodesPage](t, env.api.GetCtx(ctx, "/api/v2/libraries/"+libraryID+"/nodes?parent_id="+folderID, owner.header)); len(nested.Items) != 1 {
		t.Fatalf("nested page = %+v", nested)
	}

	renameURL := "/api/v2/nodes/" + folderID
	rename := func(token string, revision int) map[string]any {
		return map[string]any{"metadata": env.metadata(t, key, owner.public, libraryID, folderID, 1, revision), "name_token": token}
	}
	requireStatus(t, env.api.PatchCtx(ctx, renameURL, owner.header, `If-Match: "1"`, rename(siblingToken, 2)), http.StatusConflict)
	requireStatus(t, env.api.PatchCtx(ctx, renameURL, owner.header, `If-Match: "1"`, rename(random(t, 32), 3)), http.StatusUnprocessableEntity)
	renamedToken := random(t, 32)
	response = env.api.PatchCtx(ctx, renameURL, owner.header, `If-Match: "1"`, rename(renamedToken, 2))
	requireStatus(t, response, http.StatusOK)
	if renamed := decode[EncryptedNode](t, response); renamed.MetadataRevision != "2" || renamed.NameToken != renamedToken {
		t.Fatalf("renamed folder = %+v", renamed)
	}
	requireStatus(t, env.api.PatchCtx(ctx, renameURL, owner.header, `If-Match: "1"`, rename(random(t, 32), 2)), http.StatusConflict)
	requireStatus(t, env.api.PatchCtx(ctx, "/api/v2/nodes/"+rootID, owner.header, `If-Match: "1"`,
		map[string]any{"metadata": env.metadata(t, key, owner.public, libraryID, rootID, 1, 2), "name_token": random(t, 32)}), http.StatusUnprocessableEntity)

	libraryURL := "/api/v2/libraries/" + libraryID
	response = env.api.PatchCtx(ctx, libraryURL, owner.header, `If-Match: "1"`,
		map[string]any{"root_metadata": env.metadata(t, key, owner.public, libraryID, rootID, 1, 2)})
	requireStatus(t, response, http.StatusOK)
	if renamed := decode[EncryptedLibrary](t, response); renamed.MetadataRevision != "2" {
		t.Fatalf("renamed library = %+v", renamed)
	}
	requireStatus(t, env.api.PatchCtx(ctx, libraryURL, owner.header, `If-Match: "1"`,
		map[string]any{"root_metadata": env.metadata(t, key, owner.public, libraryID, rootID, 1, 3)}), http.StatusConflict)

	requireStatus(t, env.api.DeleteCtx(ctx, libraryURL, other.header), http.StatusNotFound)
	requireStatus(t, env.api.DeleteCtx(ctx, libraryURL, owner.header), http.StatusNoContent)
	if listed := decode[[]EncryptedLibrary](t, env.api.GetCtx(ctx, "/api/v2/libraries", owner.header)); len(listed) != 0 {
		t.Fatalf("encrypted libraries after deletion = %+v", listed)
	}
	events, err := env.queries.ListAuditEvents(ctx, db.ListAuditEventsParams{UserID: owner.id, Action: "folder.renamed", PageLimit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("folder rename events = %d, err = %v", len(events), err)
	}
}
