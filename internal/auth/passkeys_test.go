package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	flagUserPresent    byte = 0x01
	flagUserVerified   byte = 0x04
	flagBackupEligible byte = 0x08
	flagBackupState    byte = 0x10
	flagAttestedData   byte = 0x40
)

var base64URL = base64.RawURLEncoding

// testAuthenticator creates ES256 passkeys with "none" attestation.
type testAuthenticator struct {
	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
	signCount    uint32
	origin       string
	flags        byte
}

func newTestAuthenticator(t *testing.T) *testAuthenticator {
	t.Helper()
	return &testAuthenticator{
		key: newTestKey(t), credentialID: randomTestBytes(32), origin: testOrigin,
		flags: flagUserPresent | flagUserVerified | flagBackupEligible | flagBackupState,
	}
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func randomTestBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func (a *testAuthenticator) register(t *testing.T, options json.RawMessage) map[string]any {
	t.Helper()
	var creation struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(options, &creation); err != nil {
		t.Fatal(err)
	}
	handle, err := base64URL.DecodeString(creation.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.userHandle = handle
	point, err := a.key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	coseKey, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{
			KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256),
		},
		Curve: int64(webauthncose.P256), XCoord: point[1:33], YCoord: point[33:],
	})
	if err != nil {
		t.Fatal(err)
	}
	attested := make([]byte, 16)
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credentialID)))
	attested = append(attested, a.credentialID...)
	attested = append(attested, coseKey...)
	object, err := webauthncbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": a.authenticatorData(a.flags|flagAttestedData, attested),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.credential(map[string]any{
		"clientDataJSON":    base64URL.EncodeToString(a.clientData(t, "webauthn.create", options)),
		"attestationObject": base64URL.EncodeToString(object),
		"transports":        []string{"internal"},
	})
}

func (a *testAuthenticator) login(t *testing.T, options json.RawMessage) map[string]any {
	t.Helper()
	a.signCount++
	authenticatorData := a.authenticatorData(a.flags, nil)
	clientData := a.clientData(t, "webauthn.get", options)
	clientDataHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(authenticatorData, clientDataHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return a.credential(map[string]any{
		"clientDataJSON":    base64URL.EncodeToString(clientData),
		"authenticatorData": base64URL.EncodeToString(authenticatorData),
		"signature":         base64URL.EncodeToString(signature),
		"userHandle":        base64URL.EncodeToString(a.userHandle),
	})
}

func (a *testAuthenticator) authenticatorData(flags byte, attested []byte) []byte {
	rpIDHash := sha256.Sum256([]byte("localhost"))
	data := append(rpIDHash[:], flags)
	data = binary.BigEndian.AppendUint32(data, a.signCount)
	return append(data, attested...)
}

func (a *testAuthenticator) clientData(t *testing.T, ceremony string, options json.RawMessage) []byte {
	t.Helper()
	var parsed struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(options, &parsed); err != nil || parsed.Challenge == "" {
		t.Fatalf("options lack a challenge: %s", options)
	}
	data, err := json.Marshal(map[string]any{
		"type": ceremony, "challenge": parsed.Challenge, "origin": a.origin, "crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (a *testAuthenticator) credential(response map[string]any) map[string]any {
	credentialID := base64URL.EncodeToString(a.credentialID)
	return map[string]any{
		"id": credentialID, "rawId": credentialID, "type": "public-key", "authenticatorAttachment": "platform",
		"clientExtensionResults": map[string]any{}, "response": response,
	}
}

func passkeyOptions(t *testing.T, api humatest.TestAPI, path string, args ...any) json.RawMessage {
	t.Helper()
	response := api.PostCtx(t.Context(), path, args...)
	requireStatus(t, response, http.StatusOK)
	return json.RawMessage(response.Body.Bytes())
}

func listPasskeys(t *testing.T, api humatest.TestAPI, cookie string) []Passkey {
	t.Helper()
	response := api.GetCtx(t.Context(), "/api/v1/auth/passkeys", cookie)
	requireStatus(t, response, http.StatusOK)
	var passkeys []Passkey
	if err := json.Unmarshal(response.Body.Bytes(), &passkeys); err != nil {
		t.Fatal(err)
	}
	return passkeys
}

func decodePasskey(t *testing.T, body []byte) Passkey {
	t.Helper()
	var passkey Passkey
	if err := json.Unmarshal(body, &passkey); err != nil {
		t.Fatal(err)
	}
	return passkey
}

func testPasskeys(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// Advance the clock on each limiter check. Otherwise the login IP limit rejects the ceremonies.
	now := time.Now()
	_, api, _ := newTestAPI(t, pool, true, func() time.Time {
		now = now.Add(time.Minute)
		return now
	})
	const (
		registrationOptions = "/api/v1/auth/passkeys/registration/options"
		create              = "/api/v1/auth/passkeys"
		loginOptions        = "/api/v1/auth/passkeys/login/options"
		login               = "/api/v1/auth/passkeys/login"
	)
	password := map[string]string{"password": testPassword}
	response := api.PostCtx(t.Context(), "/api/v1/auth/register", registration("passkey@example.com"))
	requireStatus(t, response, http.StatusCreated)
	owner := "Cookie: " + responseCookie(t, response).String()
	response = api.PostCtx(t.Context(), "/api/v1/auth/register", registration("intruder@example.com"))
	requireStatus(t, response, http.StatusCreated)
	intruder := "Cookie: " + responseCookie(t, response).String()

	if passkeys := listPasskeys(t, api, owner); len(passkeys) != 0 {
		t.Fatalf("new user has passkeys: %+v", passkeys)
	}
	requireStatus(t, api.PostCtx(t.Context(), registrationOptions, password), http.StatusUnauthorized)
	requireStatus(t, api.PostCtx(t.Context(), registrationOptions, map[string]string{"password": "wrong password"}, owner),
		http.StatusUnprocessableEntity)

	options := passkeyOptions(t, api, registrationOptions, password, owner)
	var creation struct {
		RP struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"user"`
		Selection struct {
			ResidentKey      string `json:"residentKey"`
			UserVerification string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Attestation string `json:"attestation"`
	}
	if err := json.Unmarshal(options, &creation); err != nil {
		t.Fatal(err)
	}
	if handle, err := base64URL.DecodeString(creation.User.ID); err != nil || len(handle) != passkeyHandleBytes ||
		creation.RP.ID != "localhost" || creation.User.Name != "passkey@example.com" ||
		creation.Selection.ResidentKey != "required" || creation.Selection.UserVerification != "required" ||
		creation.Attestation != "none" {
		t.Fatalf("unexpected creation options: %s", options)
	}

	authenticator := newTestAuthenticator(t)
	body := map[string]any{"name": "  Laptop  ", "credential": authenticator.register(t, options)}
	response = api.PostCtx(t.Context(), create, body, owner)
	requireStatus(t, response, http.StatusCreated)
	created := decodePasskey(t, response.Body.Bytes())
	if !id.Valid(id.Passkey, created.ID) || created.Name != "Laptop" || !created.Synced || created.LastUsedAt != nil {
		t.Fatalf("unexpected passkey: %+v", created)
	}
	requireStatus(t, api.PostCtx(t.Context(), create, body, owner), http.StatusUnprocessableEntity)

	options = passkeyOptions(t, api, registrationOptions, password, owner)
	var exclusions struct {
		Credentials []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
	}
	if err := json.Unmarshal(options, &exclusions); err != nil {
		t.Fatal(err)
	}
	if len(exclusions.Credentials) != 1 || exclusions.Credentials[0].ID != base64URL.EncodeToString(authenticator.credentialID) {
		t.Fatalf("registration options do not exclude the existing passkey: %s", options)
	}
	body = map[string]any{"name": "Again", "credential": authenticator.register(t, options)}
	requireStatus(t, api.PostCtx(t.Context(), create, body, owner), http.StatusConflict)

	options = passkeyOptions(t, api, registrationOptions, password, owner)
	body = map[string]any{"name": "Stolen", "credential": newTestAuthenticator(t).register(t, options)}
	requireStatus(t, api.PostCtx(t.Context(), create, body, intruder), http.StatusUnprocessableEntity)
	requireStatus(t, api.PostCtx(t.Context(), create, map[string]any{"name": "Invalid", "credential": map[string]string{}}, owner),
		http.StatusUnprocessableEntity)

	options = passkeyOptions(t, api, loginOptions)
	var request struct {
		RPID             string `json:"rpId"`
		UserVerification string `json:"userVerification"`
		AllowCredentials []any  `json:"allowCredentials"`
	}
	if err := json.Unmarshal(options, &request); err != nil {
		t.Fatal(err)
	}
	if request.RPID != "localhost" || request.UserVerification != "required" || len(request.AllowCredentials) != 0 {
		t.Fatalf("unexpected request options: %s", options)
	}
	assertion := authenticator.login(t, options)
	response = api.PostCtx(t.Context(), login, assertion)
	requireStatus(t, response, http.StatusOK)
	assertUser(t, response, "passkey@example.com")
	requireStatus(t, api.GetCtx(t.Context(), "/api/v1/auth/me", "Cookie: "+responseCookie(t, response).String()), http.StatusOK)
	requireStatus(t, api.PostCtx(t.Context(), login, assertion), http.StatusUnauthorized)

	if passkeys := listPasskeys(t, api, owner); len(passkeys) != 1 || passkeys[0].LastUsedAt == nil {
		t.Fatalf("sign-in did not record passkey use: %+v", passkeys)
	}
	user, err := db.New(pool).GetUserByEmail(t.Context(), "passkey@example.com")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := db.New(pool).ListPasskeys(t.Context(), db.ListPasskeysParams{UserID: user.ID, RpID: "localhost"})
	if err != nil || len(stored) != 1 || stored[0].SignCount != 1 {
		t.Fatalf("sign-in did not store the sign counter: %+v, %v", stored, err)
	}

	for _, tc := range []struct {
		name   string
		change func(*testAuthenticator)
	}{
		{"wrong origin", func(a *testAuthenticator) { a.origin = "https://attacker.example" }},
		{"no user verification", func(a *testAuthenticator) { a.flags &^= flagUserVerified }},
		{"unknown credential", func(a *testAuthenticator) { a.credentialID = randomTestBytes(32) }},
		{"unknown user", func(a *testAuthenticator) { a.userHandle = randomTestBytes(passkeyHandleBytes) }},
		{"wrong key", func(a *testAuthenticator) { a.key = newTestKey(t) }},
		{"sign counter regression", func(a *testAuthenticator) { a.signCount = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := *authenticator
			tc.change(&changed)
			response := api.PostCtx(t.Context(), login, changed.login(t, passkeyOptions(t, api, loginOptions)))
			requireStatus(t, response, http.StatusUnauthorized)
			if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected passkey created a cookie")
			}
		})
	}
	requireStatus(t, api.PostCtx(t.Context(), login, map[string]string{"id": "invalid"}), http.StatusUnauthorized)

	path := "/api/v1/auth/passkeys/" + created.ID
	requireStatus(t, api.PatchCtx(t.Context(), path, map[string]string{"name": "Phone"}, intruder), http.StatusNotFound)
	requireStatus(t, api.PatchCtx(t.Context(), path, map[string]string{"name": "   "}, owner), http.StatusUnprocessableEntity)
	response = api.PatchCtx(t.Context(), path, map[string]string{"name": " Phone "}, owner)
	requireStatus(t, response, http.StatusOK)
	if renamed := decodePasskey(t, response.Body.Bytes()); renamed.ID != created.ID || renamed.Name != "Phone" {
		t.Fatalf("unexpected renamed passkey: %+v", renamed)
	}
	if passkeys := listPasskeys(t, api, intruder); len(passkeys) != 0 {
		t.Fatalf("user sees passkeys of another user: %+v", passkeys)
	}

	requireStatus(t, api.DeleteCtx(t.Context(), path, intruder), http.StatusNotFound)
	requireStatus(t, api.DeleteCtx(t.Context(), path, owner), http.StatusNoContent)
	requireStatus(t, api.DeleteCtx(t.Context(), path, owner), http.StatusNotFound)
	response = api.PostCtx(t.Context(), login, authenticator.login(t, passkeyOptions(t, api, loginOptions)))
	requireStatus(t, response, http.StatusUnauthorized)
}

func TestPasskeysUnconfigured(t *testing.T) {
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	_, api := humatest.New(t, cfg)
	service, err := New(nil, Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	service.Register(api)
	requireStatus(t, api.PostCtx(t.Context(), "/api/v1/auth/passkeys/login/options"), http.StatusServiceUnavailable)
}
