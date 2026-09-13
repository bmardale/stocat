package testutil_test

import (
	"testing"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
)

func TestNewPostgres(t *testing.T) {
	for _, name := range []string{"first database", "second database"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pool := testutil.NewPostgres(t)
			queries := db.New(pool)
			user, err := queries.CreateUser(t.Context(), db.CreateUserParams{
				PublicID:     id.New(id.User),
				Name:         "Test User",
				Email:        "test@example.com",
				PasswordHash: "test-only-hash",
			})
			if err != nil {
				t.Fatalf("create user: %v", err)
			}
			if user.ID != 1 || !id.Valid(id.User, user.PublicID) || !user.CreatedAt.Valid {
				t.Fatalf("unexpected database defaults: %+v", user)
			}
			got, err := queries.GetUserByEmail(t.Context(), user.Email)
			if err != nil {
				t.Fatalf("get user: %v", err)
			}
			if got != user {
				t.Fatalf("got %+v, want %+v", got, user)
			}
		})
	}
}
