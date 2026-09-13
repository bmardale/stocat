package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testPassword = "correct horse battery staple"

func newTestAPI(t *testing.T, pool *pgxpool.Pool, secure bool, clocks ...func() time.Time) (http.Handler, humatest.TestAPI, *Service) {
	t.Helper()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	router, api := humatest.New(t, cfg)
	now := time.Now()
	clock := func() time.Time { return now }
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	service, err := New(pool, Config{SecureCookies: secure, Logger: slog.New(slog.DiscardHandler), RateLimitClock: clock})
	if err != nil {
		t.Fatal(err)
	}
	service.Register(api)
	return router, api, service
}

func credentials(email string) map[string]string {
	return map[string]string{"email": email, "password": testPassword}
}

func registration(email string) map[string]string {
	body := credentials(email)
	body["name"] = "Test User"
	return body
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
	}
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	result := response.Result()
	defer func() {
		if err := result.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	cookies := result.Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName {
		t.Fatalf("unexpected cookies: %v", cookies)
	}
	return cookies[0]
}

func assertUser(t *testing.T, response *httptest.ResponseRecorder, email string) User {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if keys, want := slices.Sorted(maps.Keys(fields)), []string{"email", "id", "is_admin", "name"}; !slices.Equal(keys, want) {
		t.Errorf("response fields = %v, want %v", keys, want)
	}
	var user User
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if !id.Valid(id.User, user.PublicID) || user.Email != email || user.Name != "Test User" || user.IsAdmin {
		t.Fatalf("unexpected user: %+v", user)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("user response can be cached")
	}
	return user
}

func TestAuth(t *testing.T) {
	pool := testutil.NewPostgres(t)
	t.Run("lifecycle", func(t *testing.T) { testLifecycle(t, pool) })
	t.Run("invalid requests", func(t *testing.T) { testInvalidRequests(t, pool) })
	t.Run("expired and revoked sessions", func(t *testing.T) { testExpiredSessions(t, pool) })
	t.Run("user agent", func(t *testing.T) { testUserAgent(t, pool) })
	t.Run("concurrent registration", func(t *testing.T) { testConcurrentRegistration(t, pool) })
	t.Run("database failure", func(t *testing.T) { testDatabaseFailure(t, pool) })
	t.Run("migration rollback", func(t *testing.T) { testMigrationRollback(t, pool) })
}

func testLifecycle(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	router, api, service := newTestAPI(t, pool, true)
	queries := db.New(pool)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test User","email":"User@Example.com","password":"`+testPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "stocat-test")
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	request.Header.Set("X-Real-IP", "203.0.113.98")
	request.RemoteAddr = "[2001:db8::1]:1234"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	requireStatus(t, response, http.StatusCreated)
	user := assertUser(t, response, "user@example.com")
	cookie := responseCookie(t, response)
	if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.Domain != "" ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatalf("unexpected cookie: %s", cookie)
	}
	session, err := queries.GetSession(t.Context(), tokenHash(cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	if session.UserAgent != "stocat-test" || session.IpAddress == nil || session.IpAddress.String() != "2001:db8::1" {
		t.Fatalf("unexpected metadata: %+v", session)
	}
	if !session.ExpiresAt.Time.Equal(cookie.Expires) || time.Until(cookie.Expires) < sessionLifetime-time.Minute {
		t.Fatal("session expiry differs from cookie expiry")
	}
	stored, err := queries.GetUserByID(t.Context(), session.UserID)
	if err != nil || !verifyPassword(testPassword, stored.PasswordHash) {
		t.Fatalf("password was not hashed: %v", err)
	}
	protected := service.Protected(api, "/private")
	huma.Register(huma.NewGroup(protected, "/nested"), huma.Operation{
		OperationID: "private-user", Method: http.MethodGet, Path: "/user",
	}, func(ctx context.Context, _ *struct{}) (*userOutput, error) {
		current, ok := UserFromContext(ctx)
		if !ok || current.ID != stored.ID || current.PublicID != user.PublicID {
			t.Error("authenticated user is missing from context")
		}
		return &userOutput{Body: current}, nil
	})
	if _, ok := UserFromContext(t.Context()); ok {
		t.Fatal("user escaped the request context")
	}
	for _, path := range []string{"/auth/me", "/private/nested/user"} {
		requireStatus(t, api.GetCtx(t.Context(), path), http.StatusUnauthorized)
		response = api.GetCtx(t.Context(), path, "Cookie: "+cookie.String())
		requireStatus(t, response, http.StatusOK)
		assertUser(t, response, "user@example.com")
	}
	_, restarted, _ := newTestAPI(t, pool, true)
	requireStatus(t, restarted.GetCtx(t.Context(), "/auth/me", "Cookie: "+cookie.String()), http.StatusOK)
	response = api.PostCtx(t.Context(), "/auth/login", credentials(" USER@example.com "), "Cookie: "+cookie.String())
	requireStatus(t, response, http.StatusOK)
	assertUser(t, response, "user@example.com")
	fresh := responseCookie(t, response)
	if fresh.Value == cookie.Value {
		t.Fatal("login reused the old token")
	}
	requireStatus(t, api.GetCtx(t.Context(), "/auth/me", "Cookie: "+cookie.String()), http.StatusUnauthorized)
	if _, err := queries.GetSession(t.Context(), tokenHash(cookie.Value)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old session was not deleted: %v", err)
	}
	response = api.PostCtx(t.Context(), "/auth/login", credentials("user@example.com"))
	requireStatus(t, response, http.StatusOK)
	otherDevice := responseCookie(t, response)
	response = api.PostCtx(t.Context(), "/auth/logout", "Cookie: "+fresh.String())
	requireStatus(t, response, http.StatusNoContent)
	cleared := responseCookie(t, response)
	if cleared.Value != "" || cleared.MaxAge != -1 || !cleared.Expires.Before(time.Now()) || !cleared.HttpOnly || !cleared.Secure {
		t.Fatalf("cookie was not cleared: %s", cleared)
	}
	if response.Body.Len() != 0 {
		t.Fatal("logout returned a body")
	}
	if _, err := queries.GetSession(t.Context(), tokenHash(fresh.Value)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("logout did not delete the session: %v", err)
	}
	requireStatus(t, api.GetCtx(t.Context(), "/auth/me", "Cookie: "+fresh.String()), http.StatusUnauthorized)
	requireStatus(t, api.GetCtx(t.Context(), "/auth/me", "Cookie: "+otherDevice.String()), http.StatusOK)
	for _, header := range []string{"Cookie: " + fresh.String(), "Cookie: " + CookieName + "=invalid", "Cookie: unrelated=1"} {
		requireStatus(t, api.PostCtx(t.Context(), "/auth/logout", header), http.StatusNoContent)
	}
}

func testInvalidRequests(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, api, _ := newTestAPI(t, pool, false)
	response := api.PostCtx(t.Context(), "/auth/register", registration("validation@example.com"))
	requireStatus(t, response, http.StatusCreated)
	if responseCookie(t, response).Secure {
		t.Fatal("development cookie requires HTTPS")
	}
	for _, tc := range []struct {
		name, path string
		body       map[string]string
		status     int
	}{
		{"duplicate", "/auth/register", registration("VALIDATION@example.com"), http.StatusConflict},
		{"padded duplicate", "/auth/register", registration(" validation@example.com\t"), http.StatusConflict},
		{"missing password", "/auth/register", map[string]string{"name": "Test", "email": "missing@example.com"}, http.StatusUnprocessableEntity},
		{"missing name", "/auth/register", credentials("missing-name@example.com"), http.StatusUnprocessableEntity},
		{"missing email", "/auth/login", map[string]string{"password": testPassword}, http.StatusUnprocessableEntity},
		{"short password", "/auth/register", map[string]string{"name": "Test", "email": "short@example.com", "password": "secret"}, http.StatusUnprocessableEntity},
		{"long password", "/auth/register", map[string]string{"name": "Test", "email": "long@example.com", "password": strings.Repeat("x", 1025)}, http.StatusUnprocessableEntity},
		{"blank name", "/auth/register", map[string]string{"name": "  ", "email": "blank@example.com", "password": testPassword}, http.StatusUnprocessableEntity},
		{"invalid email", "/auth/register", registration("invalid"), http.StatusUnprocessableEntity},
		{"unknown user", "/auth/login", credentials("unknown@example.com"), http.StatusUnauthorized},
		{"wrong password", "/auth/login", map[string]string{"email": "validation@example.com", "password": "incorrect password"}, http.StatusUnauthorized},
		{"empty password", "/auth/login", map[string]string{"email": "validation@example.com", "password": ""}, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, api, _ := newTestAPI(t, pool, false)
			response := api.PostCtx(t.Context(), tc.path, tc.body)
			requireStatus(t, response, tc.status)
			if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("failed request created a cookie")
			}
			if password := tc.body["password"]; password != "" && strings.Contains(response.Body.String(), password) {
				t.Fatal("error response exposes the password")
			}
		})
	}
	queries := db.New(pool)
	for _, email := range []string{"short@example.com", "long@example.com", "blank@example.com"} {
		if _, err := queries.GetUserByEmail(t.Context(), email); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("invalid registration persisted user: %v", err)
		}
	}
	for _, token := range []string{"", "bad", strings.Repeat("z", 64), strings.Repeat("a", 64)} {
		requireStatus(t, api.GetCtx(t.Context(), "/auth/me", "Cookie: "+CookieName+"="+token), http.StatusUnauthorized)
	}
}

func testExpiredSessions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, api, _ := newTestAPI(t, pool, true)
	response := api.PostCtx(t.Context(), "/auth/register", registration("expired@example.com"))
	requireStatus(t, response, http.StatusCreated)
	cookie := responseCookie(t, response)
	queries := db.New(pool)
	session, err := queries.GetSession(t.Context(), tokenHash(cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.DeleteSession(t.Context(), session.TokenHash); err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddr("127.0.0.1")
	if err := queries.CreateSession(t.Context(), db.CreateSessionParams{
		TokenHash: session.TokenHash, UserID: session.UserID, UserAgent: "test", IpAddress: &addr,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, api.GetCtx(t.Context(), "/auth/me", "Cookie: "+cookie.String()), http.StatusUnauthorized)
	requireStatus(t, api.PostCtx(t.Context(), "/auth/logout", "Cookie: "+cookie.String()), http.StatusNoContent)
}

func testUserAgent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	router, _, _ := newTestAPI(t, pool, true)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test User","email":"agent@example.com","password":"`+testPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "\xff"+strings.Repeat("é", 1000))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	requireStatus(t, response, http.StatusCreated)
	session, err := db.New(pool).GetSession(t.Context(), tokenHash(responseCookie(t, response).Value))
	if err != nil {
		t.Fatal(err)
	}
	if len(session.UserAgent) > maxUserAgentBytes || !strings.HasPrefix(session.UserAgent, "\uFFFDé") {
		t.Fatalf("unexpected user agent: %q", session.UserAgent)
	}
}

func TestTruncateUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name, userAgent, want string
	}{
		{"short", "stocat-test", "stocat-test"},
		{"limit", strings.Repeat("a", maxUserAgentBytes), strings.Repeat("a", maxUserAgentBytes)},
		{"long", strings.Repeat("a", maxUserAgentBytes+1), strings.Repeat("a", maxUserAgentBytes)},
		{"split rune", "a" + strings.Repeat("é", maxUserAgentBytes/2), "a" + strings.Repeat("é", maxUserAgentBytes/2-1)},
		{"invalid UTF-8", "a\xffb", "a\uFFFDb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateUserAgent(tc.userAgent); got != tc.want {
				t.Fatalf("truncateUserAgent = %q, want %q", got, tc.want)
			}
		})
	}
}

func testConcurrentRegistration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	router, _, _ := newTestAPI(t, pool, true)
	statuses := make(chan int, 2)
	for range 2 {
		go func() {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/register",
				strings.NewReader(`{"name":"Test User","email":"race@example.com","password":"`+testPassword+`"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	first, second := <-statuses, <-statuses
	counts := map[int]int{first: 1}
	counts[second]++
	if counts[http.StatusCreated] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent status codes = %d, %d", first, second)
	}
}

func TestOpenAPI(t *testing.T) {
	_, api, service := newTestAPI(t, nil, true)
	group := service.Protected(api, "/private")
	huma.Register(group, huma.Operation{OperationID: "private", Method: http.MethodGet, Path: "/check"},
		func(context.Context, *struct{}) (*struct{}, error) { return &struct{}{}, nil })
	for path, item := range api.OpenAPI().Paths {
		op := item.Post
		if item.Get != nil {
			op = item.Get
		}
		if !strings.HasPrefix(path, "/auth/") && path != "/private/check" {
			continue
		}
		if strings.HasPrefix(path, "/auth/") && (len(op.Tags) != 1 || op.Tags[0] != "Auth") {
			t.Errorf("route %s lacks the Auth tag", path)
		}
		if response := op.Responses["429"]; response == nil || response.Headers["Retry-After"] == nil || response.Content[apierr.ContentType] == nil {
			t.Errorf("route %s lacks the rate limit response", path)
		}
		if path == "/auth/me" || path == "/private/check" {
			if len(op.Security) != 1 || op.Security[0][securityScheme] == nil || op.Responses["401"] == nil {
				t.Errorf("route %s lacks session security or the 401 response", path)
			}
		} else if len(op.Security) != 0 {
			t.Errorf("public route %s requires authentication", path)
		}
	}
	scheme := api.OpenAPI().Components.SecuritySchemes[securityScheme]
	if scheme == nil || scheme.Type != "apiKey" || scheme.In != "cookie" || scheme.Name != CookieName {
		t.Fatalf("incorrect security scheme: %+v", scheme)
	}
	schema := api.OpenAPI().Components.Schemas.Map()["User"]
	if schema == nil {
		t.Fatal("user schema is missing")
	}
	fields := slices.Sorted(maps.Keys(schema.Properties))
	if want := []string{"email", "id", "is_admin", "name"}; !slices.Equal(fields, want) {
		t.Fatalf("user schema fields = %v, want %v", fields, want)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	_, api, _ := newTestAPI(t, nil, true)
	for _, path := range []string{"/auth/register", "/auth/login"} {
		t.Run(path, func(t *testing.T) {
			body := strings.NewReader(`{"password":"` + strings.Repeat("x", 8192) + `"}`)
			requireStatus(t, api.PostCtx(t.Context(), path, "Content-Type: application/json", body), http.StatusRequestEntityTooLarge)
		})
	}
}
