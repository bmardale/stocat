package auth

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bmardale/stocat/db/migrations"
	"github.com/bmardale/stocat/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func testDatabaseFailure(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	closed, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	_, api, _ := newTestAPI(t, closed, true)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/auth/register", registration("unavailable@example.com")},
		{http.MethodPost, "/api/v1/auth/login", credentials("user@example.com")},
		{http.MethodGet, "/api/v1/auth/me", nil},
		{http.MethodPost, "/api/v1/auth/logout", nil},
		{http.MethodGet, "/api/v1/auth/sessions", nil},
		{http.MethodDelete, "/api/v1/auth/sessions", nil},
		{http.MethodDelete, "/api/v1/auth/sessions/ses_01K4W9T5V8QK3M7ZB0YHXC2FNE", nil},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			args := []any{"Cookie: " + CookieName + "=" + strings.Repeat("a", 64)}
			if tc.body != nil {
				args = append(args, tc.body)
			}
			response := api.DoCtx(t.Context(), tc.method, tc.path, args...)
			requireStatus(t, response, http.StatusInternalServerError)
			if response.Header().Get("Set-Cookie") != "" || strings.Contains(response.Body.String(), "closed") {
				t.Fatal("database failure changed the cookie or exposed database details")
			}
		})
	}
}

func testMigrationRollback(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	results, err := provider.DownTo(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[len(results)-1].Source.Version != 2 {
		t.Fatalf("rolled back %d migrations, want migrations down to 2", len(results))
	}
	var users int
	if err := sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatalf("rollback damaged the users table: %v", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("reapply migration: %v", err)
	}
	if _, err := db.New(pool).GetUserByEmail(t.Context(), "user@example.com"); err != nil {
		t.Fatalf("reapplied migration is not usable: %v", err)
	}
}
