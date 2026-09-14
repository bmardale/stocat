package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
)

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"grant"},
		{"promote", "user@example.com"},
		{"grant", "user@example.com", "extra"},
	} {
		if err := run(t.Context(), args, "postgres://localhost/unused", &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("run(%q) error = %v, want errUsage", args, err)
		}
	}
	if err := run(t.Context(), []string{"grant", "user@example.com"}, "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("run() without DATABASE_URL error = %v", err)
	}
}

func TestRun(t *testing.T) {
	pool := testutil.NewPostgres(t)
	queries := db.New(pool)
	if _, err := queries.CreateUser(t.Context(), db.CreateUserParams{
		PublicID: id.New(id.User), Name: "Ada", Email: "ada@example.com", PasswordHash: "unused",
	}); err != nil {
		t.Fatal(err)
	}
	url := pool.Config().ConnString()

	for _, tc := range []struct {
		command, output string
		admin           bool
	}{
		{"grant", "ada@example.com is now an administrator.\n", true},
		{"grant", "ada@example.com is now an administrator.\n", true},
		{"revoke", "ada@example.com is no longer an administrator.\n", false},
	} {
		var output bytes.Buffer
		if err := run(t.Context(), []string{tc.command, " ADA@example.com "}, url, &output); err != nil {
			t.Fatalf("%s: %v", tc.command, err)
		}
		if output.String() != tc.output {
			t.Errorf("%s output = %q, want %q", tc.command, output.String(), tc.output)
		}
		user, err := queries.GetUserByEmail(t.Context(), "ada@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if user.IsAdmin != tc.admin {
			t.Errorf("after %s, is_admin = %v, want %v", tc.command, user.IsAdmin, tc.admin)
		}
	}

	user, err := queries.GetUserByEmail(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	events, err := queries.ListAuditEvents(t.Context(), db.ListAuditEventsParams{UserID: user.ID, PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	wantActions := []audit.Action{audit.AccountAdminRevoked, audit.AccountAdminGranted, audit.AccountAdminGranted}
	if len(events) != len(wantActions) {
		t.Fatalf("audit events = %+v, want %v", events, wantActions)
	}
	for i, event := range events {
		if event.Action != string(wantActions[i]) || event.ActorType != "system" || event.SubjectPublicID.String != user.PublicID {
			t.Errorf("audit event %d = %+v, want %s by the system", i, event, wantActions[i])
		}
	}

	err = run(t.Context(), []string{"grant", "missing@example.com"}, url, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `no user has the email "missing@example.com"`) {
		t.Fatalf("run() for a missing user error = %v", err)
	}
}
