package auth

import (
	"context"
	"errors"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/ratelimit"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

// RecentAuthenticationWindow is the time after sign-in or password confirmation that allows sensitive changes.
const RecentAuthenticationWindow = 10 * time.Minute

type reauthenticateInput struct {
	Body struct {
		Password string `json:"password" writeOnly:"true"`
	}
}

func (s *Service) reauthenticate(ctx context.Context, input *reauthenticateInput) (*struct{}, error) {
	current, _ := UserFromContext(ctx)
	if delay := s.loginEmail.Allow(normalizeEmail(current.Email)); delay > 0 {
		return nil, ratelimit.Error(delay)
	}
	if input.Body.Password == "" || len(input.Body.Password) > 1024 {
		return nil, huma.Error422UnprocessableEntity("The password is incorrect.")
	}
	session, _ := ctx.Value(sessionKey{}).([]byte)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		user, err := queries.GetUserByIDForUpdate(ctx, current.ID)
		if err != nil {
			return err
		}
		if !verifyPassword(input.Body.Password, user.PasswordHash) {
			return errCurrentPassword
		}
		updated, err := queries.RefreshSessionAuthentication(ctx, db.RefreshSessionAuthenticationParams{
			TokenHash: session, UserID: current.ID,
		})
		if err != nil {
			return err
		}
		if updated == 0 {
			return pgx.ErrNoRows
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountReauthenticated, ActorID: current.ID, Details: audit.Details{Method: audit.MethodPassword},
		})
	})
	if errors.Is(err, errCurrentPassword) {
		return nil, huma.Error422UnprocessableEntity("The password is incorrect.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error401Unauthorized("Sign in to continue.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "confirm password", err)
	}
	return &struct{}{}, nil
}

// RequireRecentAuthentication returns status 403 when the session is older than RecentAuthenticationWindow.
// Use it only in a route of a Protected group.
func (s *Service) RequireRecentAuthentication(ctx context.Context) error {
	session, _ := ctx.Value(sessionKey{}).([]byte)
	authenticatedAt, err := s.queries.GetSessionAuthenticatedAt(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return huma.Error401Unauthorized("Sign in to continue.")
	}
	if err != nil {
		return s.internalError(ctx, "load session authentication time", err)
	}
	if time.Since(authenticatedAt.Time) > RecentAuthenticationWindow {
		return huma.Error403Forbidden("Confirm your password to continue.")
	}
	return nil
}
