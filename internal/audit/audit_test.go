package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/testutil"
)

var actionPattern = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)

func TestActions(t *testing.T) {
	seen := map[Action]bool{}
	for _, action := range Actions() {
		if seen[action] {
			t.Errorf("action %q is declared more than once", action)
		}
		seen[action] = true
		if !actionPattern.MatchString(string(action)) {
			t.Errorf("action %q does not use the form resource.verb", action)
		}
		if !Valid(action) {
			t.Errorf("Valid(%q) = false", action)
		}
	}
	if Valid("unknown.action") {
		t.Error("Valid accepts an unknown action")
	}
}

func TestMiddleware(t *testing.T) {
	for _, tc := range []struct {
		name, remote, userAgent, wantIP, wantUserAgent string
	}{
		{"IPv4-mapped address", "[::ffff:192.0.2.1]:1234", "curl/8.7", "192.0.2.1", "curl/8.7"},
		{"address without port", "192.0.2.2", "", "192.0.2.2", ""},
		{"invalid address and UTF-8", "pipe", "bad\xffagent", "", "bad�agent"},
		{"long user agent", "192.0.2.3:80", strings.Repeat("é", 300), "192.0.2.3", strings.Repeat("é", 256)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got requestMetadata
			handler := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = requestFrom(r.Context())
			}))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			request.RemoteAddr = tc.remote
			request.Header.Set("User-Agent", tc.userAgent)
			handler.ServeHTTP(httptest.NewRecorder(), request)
			gotIP := ""
			if got.ip != nil {
				gotIP = got.ip.String()
			}
			if gotIP != tc.wantIP || got.userAgent != tc.wantUserAgent {
				t.Fatalf("metadata = %q, %q; want %q, %q", gotIP, got.userAgent, tc.wantIP, tc.wantUserAgent)
			}
		})
	}
}

func TestRecordAndPurge(t *testing.T) {
	pool := testutil.NewPostgres(t)
	queries := db.New(pool)
	ctx := t.Context()
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		PublicID: id.New(id.User), Name: "Ada", Email: "ada@example.com", PasswordHash: "unused",
	})
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddr("192.0.2.7")
	requestCtx := context.WithValue(ctx, requestKey{}, requestMetadata{ip: &addr, userAgent: "audit-test"})
	if err := Record(requestCtx, queries, Event{
		Action: PasskeyAdded, ActorID: user.ID, TargetID: "pky_test", Details: Details{Name: "Laptop"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, queries, Event{Action: AccountAdminGranted, SubjectID: user.ID}); err != nil {
		t.Fatal(err)
	}

	rows, err := queries.ListAuditEvents(ctx, db.ListAuditEventsParams{UserID: user.ID, PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("events = %+v, want two", rows)
	}
	system, passkey := rows[0], rows[1]
	if system.Action != string(AccountAdminGranted) || system.ActorType != "system" || system.ActorID.Valid ||
		system.SubjectPublicID.String != user.PublicID || system.IpAddress != nil {
		t.Errorf("system event = %+v", system)
	}
	var details Details
	if err := json.Unmarshal(passkey.Details, &details); err != nil {
		t.Fatal(err)
	}
	if passkey.ActorType != "user" || passkey.ActorPublicID.String != user.PublicID ||
		passkey.SubjectPublicID.String != user.PublicID || passkey.TargetID != "pky_test" ||
		passkey.IpAddress == nil || *passkey.IpAddress != addr || passkey.UserAgent != "audit-test" || details.Name != "Laptop" {
		t.Errorf("user event = %+v, details = %+v", passkey, details)
	}

	if err := Purge(ctx, queries, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rows, err = queries.ListAuditEvents(ctx, db.ListAuditEventsParams{PageLimit: 10}); err != nil || len(rows) != 2 {
		t.Fatalf("after purge of old events: %d events, %v", len(rows), err)
	}
	if err := Purge(ctx, queries, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rows, err = queries.ListAuditEvents(ctx, db.ListAuditEventsParams{PageLimit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("after purge of all events: %d events, %v", len(rows), err)
	}
}
