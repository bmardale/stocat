package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

func authRequest(t *testing.T, router http.Handler, method, path, body, remote, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	request.RemoteAddr = remote
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", remote)
	request.Header.Set("X-Real-IP", remote)
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func requireRateLimited(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	requireStatus(t, response, http.StatusTooManyRequests)
	seconds, err := strconv.Atoi(response.Header().Get("Retry-After"))
	if err != nil || seconds < 1 {
		t.Fatalf("invalid Retry-After: %q", response.Header().Get("Retry-After"))
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("rate limit response changed a cookie or allowed caching")
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), apierr.ContentType) {
		t.Fatal("rate limit response lacks the problem content type")
	}
	var problem apierr.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Status != http.StatusTooManyRequests {
		t.Fatalf("invalid problem: %s", response.Body)
	}
}

func TestCredentialIPLimits(t *testing.T) {
	for _, tc := range []struct {
		path  string
		burst int
	}{
		{"/auth/login", 10}, {"/auth/register", 3},
	} {
		t.Run(tc.path, func(t *testing.T) {
			router, _, _ := newTestAPI(t, nil, true)
			for i := range tc.burst {
				response := authRequest(t, router, http.MethodPost, tc.path, "{", fmt.Sprintf("[2001:db8::%x]:1234", i+1), "")
				requireStatus(t, response, http.StatusBadRequest)
			}
			response := authRequest(t, router, http.MethodPost, tc.path, "{", "[2001:db8::ffff]:9999", "")
			requireRateLimited(t, response)
			response = authRequest(t, router, http.MethodPost, tc.path, "{", "[2001:db8:0:1::1]:1234", "")
			requireStatus(t, response, http.StatusBadRequest)
			response = authRequest(t, router, http.MethodPost, "/auth/logout", "", "[2001:db8::1]:1234", "")
			requireStatus(t, response, http.StatusNoContent)
		})
	}
}

func TestCredentialSharedIPLimit(t *testing.T) {
	router, _, _ := newTestAPI(t, nil, true)
	for range 3 {
		requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/register", "{}", "192.0.2.1:1234", ""), http.StatusUnprocessableEntity)
	}
	for range 7 {
		requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/login", "{}", "192.0.2.1:1234", ""), http.StatusUnprocessableEntity)
	}
	requireRateLimited(t, authRequest(t, router, http.MethodPost, "/auth/login", "{}", "192.0.2.1:1234", ""))
}

func TestRejectedRegistrationKeepsSharedIPTokens(t *testing.T) {
	router, _, _ := newTestAPI(t, nil, true)
	for range 3 {
		requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/register", "{", "192.0.2.1:1234", ""), http.StatusBadRequest)
	}
	for range 10 {
		requireRateLimited(t, authRequest(t, router, http.MethodPost, "/auth/register", "{", "192.0.2.1:1234", ""))
	}
	for range 7 {
		requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/login", "{", "192.0.2.1:1234", ""), http.StatusBadRequest)
	}
	requireRateLimited(t, authRequest(t, router, http.MethodPost, "/auth/login", "{", "192.0.2.1:1234", ""))
}

func TestRejectedLoginKeepsEmailIPTokens(t *testing.T) {
	now := time.Now()
	router, _, _ := newTestAPI(t, nil, true, func() time.Time { return now })
	const body = `{"email":"user@example.com","password":""}`
	for i := range 15 {
		requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/login", body, fmt.Sprintf("192.0.2.%d:1234", i+1), ""), http.StatusUnauthorized)
	}
	for range 6 {
		requireRateLimited(t, authRequest(t, router, http.MethodPost, "/auth/login", body, "198.51.100.1:1234", ""))
	}
	now = now.Add(2 * time.Second)
	requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/login", body, "198.51.100.1:1234", ""), http.StatusUnauthorized)
}

func TestLoginEmailLimit(t *testing.T) {
	router, _, _ := newTestAPI(t, nil, true)
	for i := range 15 {
		response := authRequest(t, router, http.MethodPost, "/auth/login",
			`{"email":"User@example.com","password":""}`, fmt.Sprintf("192.0.2.%d:1234", i+1), "")
		requireStatus(t, response, http.StatusUnauthorized)
	}
	response := authRequest(t, router, http.MethodPost, "/auth/login",
		`{"email":" USER@EXAMPLE.COM ","password":""}`, "192.0.2.100:1234", "")
	requireRateLimited(t, response)
	if strings.Contains(strings.ToLower(response.Body.String()), "user@example.com") {
		t.Fatal("rate limit response exposes the email")
	}
	response = authRequest(t, router, http.MethodPost, "/auth/login",
		`{"email":"other@example.com","password":""}`, "192.0.2.101:1234", "")
	requireStatus(t, response, http.StatusUnauthorized)
}

func TestLoginEmailIPLimit(t *testing.T) {
	now := time.Now()
	router, _, _ := newTestAPI(t, nil, true, func() time.Time { return now })
	for i := range 5 {
		response := authRequest(t, router, http.MethodPost, "/auth/login",
			`{"email":"User@example.com","password":""}`, fmt.Sprintf("[2001:db8::%x]:1234", i+1), "")
		requireStatus(t, response, http.StatusUnauthorized)
	}
	response := authRequest(t, router, http.MethodPost, "/auth/login",
		`{"email":" USER@EXAMPLE.COM ","password":""}`, "[2001:db8::ffff]:5678", "")
	requireRateLimited(t, response)
	for range 100 {
		now = now.Add(12 * time.Second)
		for _, remote := range []string{"[2001:db8::1]:1234", "192.0.2.100:1234"} {
			response := authRequest(t, router, http.MethodPost, "/auth/login",
				`{"email":"user@example.com","password":""}`, remote, "")
			requireStatus(t, response, http.StatusUnauthorized)
		}
	}
}

func TestRegistrationEmailValidation(t *testing.T) {
	pool := testutil.NewPostgres(t)
	router, _, _ := newTestAPI(t, pool, true)
	for i, fields := range []map[string]string{
		{"name": "Test", "email": "validation-limit@example.com", "password": "short"},
		{"name": "Test", "email": "validation-limit@example.com", "password": "short"},
		{"name": " ", "email": "validation-limit@example.com", "password": testPassword},
		{"name": "Test", "email": "validation-limit@example.com", "password": strings.Repeat("x", 1025)},
	} {
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		response := authRequest(t, router, http.MethodPost, "/auth/register", string(body), fmt.Sprintf("192.0.2.%d:1234", i+1), "")
		requireStatus(t, response, http.StatusUnprocessableEntity)
	}
	for i, status := range []int{http.StatusCreated, http.StatusConflict, http.StatusTooManyRequests} {
		body, err := json.Marshal(registration(" VALIDATION-LIMIT@example.com "))
		if err != nil {
			t.Fatal(err)
		}
		response := authRequest(t, router, http.MethodPost, "/auth/register", string(body), fmt.Sprintf("192.0.2.%d:1234", i+100), "")
		requireStatus(t, response, status)
		if status == http.StatusTooManyRequests {
			requireRateLimited(t, response)
		}
	}
}

func TestRenamedCredentialOperations(t *testing.T) {
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	apierr.Install(&cfg)
	router, api := humatest.New(t, cfg)
	group := huma.NewGroup(api)
	group.UseSimpleModifier(func(op *huma.Operation) { op.OperationID = "renamed-" + op.OperationID })
	now := time.Now()
	service, err := New(nil, Config{RateLimitClock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	service.Register(group)
	for _, tc := range []struct {
		path, remote string
		burst        int
	}{
		{"/auth/login", "192.0.2.1:1234", 10},
		{"/auth/register", "192.0.2.2:1234", 3},
	} {
		for i := range tc.burst + 1 {
			response := authRequest(t, router, http.MethodPost, tc.path, "{", tc.remote, "")
			if i < tc.burst {
				requireStatus(t, response, http.StatusBadRequest)
			} else {
				requireRateLimited(t, response)
			}
		}
	}
}

func TestUserRateLimit(t *testing.T) {
	pool := testutil.NewPostgres(t)
	now := time.Now()
	router, api, service := newTestAPI(t, pool, true, func() time.Time { return now })
	private := service.Protected(huma.NewGroup(service.Protected(api), "/private"))
	huma.Register(private, huma.Operation{OperationID: "limited-private", Method: http.MethodGet, Path: "/check"},
		func(ctx context.Context, _ *struct{}) (*userOutput, error) {
			user, _ := UserFromContext(ctx)
			return &userOutput{Body: user}, nil
		})
	response := api.PostCtx(t.Context(), "/auth/register", registration("limited@example.com"))
	requireStatus(t, response, http.StatusCreated)
	first := responseCookie(t, response).String()
	response = api.PostCtx(t.Context(), "/auth/login", credentials("limited@example.com"))
	requireStatus(t, response, http.StatusOK)
	second := responseCookie(t, response).String()
	response = api.PostCtx(t.Context(), "/auth/register", registration("other-limit@example.com"))
	requireStatus(t, response, http.StatusCreated)
	other := responseCookie(t, response).String()
	for i := range 30 {
		path, cookie := "/auth/me", first
		if i%2 == 0 {
			path, cookie = "/private/check", second
		}
		response = authRequest(t, router, http.MethodGet, path, "", fmt.Sprintf("192.0.2.%d:1234", i+1), cookie)
		requireStatus(t, response, http.StatusOK)
	}
	for _, cookie := range []string{first, second} {
		requireRateLimited(t, authRequest(t, router, http.MethodGet, "/auth/me", "", "192.0.2.100:1234", cookie))
	}
	requireStatus(t, authRequest(t, router, http.MethodGet, "/auth/me", "", "192.0.2.100:1234", other), http.StatusOK)
	now = now.Add(500 * time.Millisecond)
	requireStatus(t, authRequest(t, router, http.MethodGet, "/private/check", "", "192.0.2.100:1234", first), http.StatusOK)
	requireRateLimited(t, authRequest(t, router, http.MethodGet, "/auth/me", "", "192.0.2.100:1234", second))
	requireStatus(t, authRequest(t, router, http.MethodPost, "/auth/logout", "", "192.0.2.100:1234", first), http.StatusNoContent)
}
