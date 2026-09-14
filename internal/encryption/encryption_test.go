package encryption

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgtype"
)

const testPassword = "correct horse battery staple"

var b64 = base64.RawURLEncoding.EncodeToString

type testEnv struct {
	api        humatest.TestAPI
	queries    *db.Queries
	deployment []byte
}

type testUser struct {
	id, token string
}

func (u testUser) header() string { return "Cookie: " + auth.CookieName + "=" + u.token }

type testIdentity struct {
	accountID  string
	generation uint64
	signing    ed25519.PrivateKey
	record     []byte
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	pool := testutil.NewPostgres(t)
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	authService, err := auth.New(pool, auth.Config{Logger: slog.New(slog.DiscardHandler), RateLimitClock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	authService.Register(api)
	New(pool, authService, slog.New(slog.DiscardHandler)).Register(authService.Protected(api, "/api/v2"))
	queries := db.New(pool)
	deployment, err := queries.GetEncryptionDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return testEnv{api: api, queries: queries, deployment: deployment.Bytes[:]}
}

func (e testEnv) signUp(t *testing.T) testUser {
	t.Helper()
	email := strings.ToLower(id.New("test")) + "@example.com"
	response := e.api.PostCtx(t.Context(), "/api/v1/auth/register", map[string]string{
		"name": "Test", "email": email, "password": testPassword,
	})
	requireStatus(t, response, http.StatusCreated)
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	result := response.Result()
	defer func() { _ = result.Body.Close() }()
	return testUser{id: user.ID, token: result.Cookies()[0].Value}
}

// expireAuthentication moves the session authentication time before the recent authentication window.
func (e testEnv) expireAuthentication(t *testing.T, user testUser) {
	t.Helper()
	raw, err := hex.DecodeString(user.token)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	session, err := e.queries.GetSession(t.Context(), hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if err = e.queries.DeleteSession(t.Context(), hash[:]); err != nil {
		t.Fatal(err)
	}
	session.AuthenticatedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	if err = e.queries.CreateSession(t.Context(), db.CreateSessionParams{
		TokenHash: session.TokenHash, PublicID: session.PublicID, UserID: session.UserID, UserAgent: session.UserAgent,
		IpAddress: session.IpAddress, ExpiresAt: session.ExpiresAt, AuthenticatedAt: session.AuthenticatedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e testEnv) record(t *testing.T, kind string, fields map[string]string) []byte {
	t.Helper()
	data, err := encryptionv2.Record{Type: kind, DeploymentID: b64(e.deployment), Fields: fields}.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (e testEnv) identity(t *testing.T, accountID string, generation uint64) testIdentity {
	t.Helper()
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return e.identityWithKey(t, accountID, generation, signing, recipient.PublicKey().Bytes(), generation)
}

func (e testEnv) identityWithKey(t *testing.T, accountID string, generation uint64, signing ed25519.PrivateKey, recipientKey []byte, keyIDGeneration uint64) testIdentity {
	t.Helper()
	keyID, err := encryptionv2.RecipientKeyID(e.deployment, accountID, keyIDGeneration, recipientKey)
	if err != nil {
		t.Fatal(err)
	}
	return testIdentity{accountID: accountID, generation: generation, signing: signing, record: e.record(t, "identity", map[string]string{
		"account_id": accountID, "generation": strconv.FormatUint(generation, 10),
		"signing_public_key":   b64(signing.Public().(ed25519.PublicKey)),
		"recipient_public_key": b64(recipientKey), "recipient_key_id": b64(keyID),
	})}
}

func sign(t *testing.T, key ed25519.PrivateKey, data []byte) SignedRecord {
	t.Helper()
	input, err := encryptionv2.SignatureInput(data)
	if err != nil {
		t.Fatal(err)
	}
	return SignedRecord{Record: b64(data), Signature: b64(ed25519.Sign(key, input))}
}

func random(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return b64(value)
}

func (e testEnv) envelope(t *testing.T, kind, accountID string, generation, revision uint64, key ed25519.PrivateKey) SignedRecord {
	t.Helper()
	fields := map[string]string{
		"account_id": accountID, "generation": strconv.FormatUint(generation, 10),
		"bundle_revision": strconv.FormatUint(revision, 10), "nonce": random(t, 24), "ciphertext": random(t, 48),
	}
	switch kind {
	case "account-password":
		fields["profile"], fields["salt"] = encryptionv2.Argon2Profile, random(t, 16)
	case "account-private":
		fields["ciphertext"] = random(t, 256)
	}
	return sign(t, key, e.record(t, kind, fields))
}

func (e testEnv) envelopes(t *testing.T, identity testIdentity, revision uint64) map[string]any {
	t.Helper()
	body := map[string]any{}
	for field, kind := range map[string]string{
		"password_envelope": "account-password", "recovery_envelope": "account-recovery", "private_envelope": "account-private",
	} {
		body[field] = e.envelope(t, kind, identity.accountID, identity.generation, revision, identity.signing)
	}
	return body
}

func (e testEnv) initialization(t *testing.T, identity testIdentity) map[string]any {
	t.Helper()
	body := e.envelopes(t, identity, 1)
	body["identity"] = sign(t, identity.signing, identity.record)
	return body
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func decodeBundle(t *testing.T, response *httptest.ResponseRecorder) Bundle {
	t.Helper()
	var bundle Bundle
	if err := json.Unmarshal(response.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestBundleLifecycle(t *testing.T) {
	env := newTestEnv(t)
	owner, other := env.signUp(t), env.signUp(t)
	ctx := t.Context()

	requireStatus(t, env.api.GetCtx(ctx, "/api/v2/encryption/bundle"), http.StatusUnauthorized)
	response := env.api.GetCtx(ctx, "/api/v2/encryption/bundle", owner.header())
	requireStatus(t, response, http.StatusOK)
	if bundle := decodeBundle(t, response); bundle.State != stateAbsent || bundle.AccountID != owner.id ||
		bundle.DeploymentID != b64(env.deployment) || len(bundle.Identities) != 0 || response.Header().Get("ETag") != "" {
		t.Fatalf("absent bundle = %+v, ETag %q", bundle, response.Header().Get("ETag"))
	}

	first := env.identity(t, owner.id, 1)
	env.expireAuthentication(t, owner)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/encryption/bundle", owner.header(), env.initialization(t, first)), http.StatusForbidden)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v1/auth/reauthenticate", owner.header(), map[string]string{"password": testPassword}), http.StatusNoContent)

	t.Run("rejects invalid initialization", func(t *testing.T) {
		_, stranger, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		recipient, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			change func(body map[string]any)
		}{
			{"identity of another account", func(body map[string]any) {
				body["identity"] = sign(t, first.signing, env.identityWithKey(t, other.id, 1, first.signing, recipient.PublicKey().Bytes(), 1).record)
			}},
			{"second generation", func(body map[string]any) {
				body["identity"] = sign(t, first.signing, env.identityWithKey(t, owner.id, 2, first.signing, recipient.PublicKey().Bytes(), 2).record)
			}},
			{"recipient key identifier of another generation", func(body map[string]any) {
				body["identity"] = sign(t, first.signing, env.identityWithKey(t, owner.id, 1, first.signing, recipient.PublicKey().Bytes(), 2).record)
			}},
			{"low-order recipient key", func(body map[string]any) {
				body["identity"] = sign(t, first.signing, env.identityWithKey(t, owner.id, 1, first.signing, make([]byte, 32), 1).record)
			}},
			{"identity signed by another key", func(body map[string]any) {
				body["identity"] = sign(t, stranger, first.record)
			}},
			{"record of another type", func(body map[string]any) {
				body["identity"] = body["recovery_envelope"]
			}},
			{"envelope of another deployment", func(body map[string]any) {
				foreign := testEnv{deployment: make([]byte, 16)}
				body["recovery_envelope"] = foreign.envelope(t, "account-recovery", owner.id, 1, 1, first.signing)
			}},
			{"envelope with the next revision", func(body map[string]any) {
				body["private_envelope"] = env.envelope(t, "account-private", owner.id, 1, 2, first.signing)
			}},
			{"envelope signed by another key", func(body map[string]any) {
				body["password_envelope"] = env.envelope(t, "account-password", owner.id, 1, 1, stranger)
			}},
			{"padded base64url", func(body map[string]any) {
				envelope := body["password_envelope"].(SignedRecord)
				envelope.Record += "="
				body["password_envelope"] = envelope
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := env.initialization(t, first)
				tc.change(body)
				requireStatus(t, env.api.PostCtx(ctx, "/api/v2/encryption/bundle", owner.header(), body), http.StatusUnprocessableEntity)
			})
		}
	})

	response = env.api.PostCtx(ctx, "/api/v2/encryption/bundle", owner.header(), env.initialization(t, first))
	requireStatus(t, response, http.StatusCreated)
	created := decodeBundle(t, response)
	if created.State != stateConfigured || created.Generation != "1" || created.BundleRevision != "1" ||
		len(created.Identities) != 1 || created.Identities[0].Record != b64(first.record) || response.Header().Get("ETag") != `"1.1"` {
		t.Fatalf("created bundle = %+v, ETag %q", created, response.Header().Get("ETag"))
	}
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/encryption/bundle", owner.header(), env.initialization(t, first)), http.StatusConflict)
	response = env.api.GetCtx(ctx, "/api/v2/encryption/bundle", other.header())
	requireStatus(t, response, http.StatusOK)
	if bundle := decodeBundle(t, response); bundle.State != stateAbsent || bundle.AccountID != other.id {
		t.Fatalf("other account bundle = %+v", bundle)
	}

	password := env.envelope(t, "account-password", owner.id, 1, 2, first.signing)
	update := map[string]any{"password_envelope": password}
	requireStatus(t, env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), update), http.StatusUnprocessableEntity)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), `If-Match: "1.0"`, update), http.StatusConflict)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), `If-Match: "1.1"`, map[string]any{}), http.StatusUnprocessableEntity)
	response = env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), `If-Match: "1.1"`, update)
	requireStatus(t, response, http.StatusOK)
	updated := decodeBundle(t, response)
	if updated.PasswordEnvelope != password.Record || updated.RecoveryEnvelope != created.RecoveryEnvelope ||
		updated.BundleRevision != "2" || response.Header().Get("ETag") != `"1.2"` {
		t.Fatalf("updated bundle = %+v, ETag %q", updated, response.Header().Get("ETag"))
	}
	requireStatus(t, env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), `If-Match: "1.1"`, update), http.StatusConflict)

	second := env.identity(t, owner.id, 2)
	previousHash, nextHash := sha256.Sum256(first.record), sha256.Sum256(second.record)
	continuity := env.record(t, "identity-continuity", map[string]string{
		"account_id": owner.id, "previous_generation": "1", "generation": "2",
		"previous_identity_hash": b64(previousHash[:]), "identity_hash": b64(nextHash[:]),
	})
	rotation := env.envelopes(t, second, 1)
	rotation["identity"] = sign(t, second.signing, second.record)
	rotation["continuity"] = sign(t, second.signing, continuity)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/encryption/identities/rotate", owner.header(), `If-Match: "1.2"`, rotation), http.StatusUnprocessableEntity)
	rotation["continuity"] = sign(t, first.signing, continuity)
	requireStatus(t, env.api.PostCtx(ctx, "/api/v2/encryption/identities/rotate", owner.header(), `If-Match: "1.1"`, rotation), http.StatusConflict)
	response = env.api.PostCtx(ctx, "/api/v2/encryption/identities/rotate", owner.header(), `If-Match: "1.2"`, rotation)
	requireStatus(t, response, http.StatusOK)
	rotated := decodeBundle(t, response)
	if rotated.Generation != "2" || rotated.BundleRevision != "1" || len(rotated.Identities) != 2 ||
		rotated.Identities[1].ContinuityRecord != b64(continuity) || rotated.Identities[0].ContinuityRecord != "" ||
		response.Header().Get("ETag") != `"2.1"` {
		t.Fatalf("rotated bundle = %+v, ETag %q", rotated, response.Header().Get("ETag"))
	}
	stale := env.envelope(t, "account-password", owner.id, 1, 3, first.signing)
	requireStatus(t, env.api.PutCtx(ctx, "/api/v2/encryption/bundle", owner.header(), `If-Match: "2.1"`, map[string]any{"password_envelope": stale}), http.StatusUnprocessableEntity)

	for action, want := range map[string]int{"encryption.initialized": 1, "encryption.bundle_updated": 1, "encryption.identity_rotated": 1} {
		user, err := env.queries.GetUserByPublicID(ctx, owner.id)
		if err != nil {
			t.Fatal(err)
		}
		events, err := env.queries.ListAuditEvents(ctx, db.ListAuditEventsParams{UserID: user.ID, Action: action, PageLimit: 10})
		if err != nil || len(events) != want {
			t.Fatalf("%s events = %d, err = %v, want %d", action, len(events), err, want)
		}
	}
}
