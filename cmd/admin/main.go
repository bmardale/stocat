// Command admin grants or revokes the administrator role of a user.
//
// Usage:
//
//	admin grant <email>
//	admin revoke <email>
//
// The command reads the database connection from DATABASE_URL.
// The change applies to the next request of each session of the user.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/jackc/pgx/v5"
)

const usage = `usage: admin grant <email>
       admin revoke <email>`

var errUsage = errors.New("invalid arguments")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv("DATABASE_URL"), os.Stdout)
	stop()
	if errors.Is(err, errUsage) {
		_, _ = fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, databaseURL string, stdout io.Writer) error {
	if len(args) != 2 || (args[0] != "grant" && args[0] != "revoke") {
		return errUsage
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, databaseURL, 1)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	grant := args[0] == "grant"
	email := strings.TrimSpace(args[1])
	action := audit.AccountAdminGranted
	if !grant {
		action = audit.AccountAdminRevoked
	}
	var user db.User
	err = db.InTx(ctx, pool, func(queries *db.Queries) error {
		var err error
		user, err = queries.SetUserAdminByEmail(ctx, db.SetUserAdminByEmailParams{Email: email, IsAdmin: grant})
		if err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{Action: action, SubjectID: user.ID, TargetID: user.PublicID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no user has the email %q", email)
	}
	if err != nil {
		return fmt.Errorf("update user %q: %w", email, err)
	}
	result := "is now an administrator"
	if !grant {
		result = "is no longer an administrator"
	}
	if _, err := fmt.Fprintf(stdout, "%s %s.\n", user.Email, result); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}
