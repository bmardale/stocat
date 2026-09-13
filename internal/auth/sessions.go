package auth

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

type Session struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	IPAddress *string   `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
	Current   bool      `json:"current" doc:"The request uses this session."`
}

type sessionsOutput struct {
	Body []Session `nullable:"false"`
}

type revokeSessionInput struct {
	ID string `path:"id" maxLength:"64"`
}

type revokeSessionOutput struct {
	// A string field lets Huma omit the header. Huma writes an empty header for a zero http.Cookie.
	SetCookie string `header:"Set-Cookie" doc:"Clears the cookie when the request revokes its own session."`
}

func (s *Service) listSessions(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
	user, _ := UserFromContext(ctx)
	current, _ := ctx.Value(sessionKey{}).([]byte)
	rows, err := s.queries.ListUserSessions(ctx, db.ListUserSessionsParams{CurrentTokenHash: current, UserID: user.ID})
	if err != nil {
		return nil, s.internalError(ctx, "list sessions", err)
	}
	output := &sessionsOutput{Body: make([]Session, 0, len(rows))}
	for _, row := range rows {
		session := Session{ID: row.PublicID, UserAgent: row.UserAgent, CreatedAt: row.CreatedAt.Time, Current: row.Current}
		if row.IpAddress != nil {
			ip := row.IpAddress.String()
			session.IPAddress = &ip
		}
		output.Body = append(output.Body, session)
	}
	return output, nil
}

func (s *Service) revokeSession(ctx context.Context, input *revokeSessionInput) (*revokeSessionOutput, error) {
	user, _ := UserFromContext(ctx)
	revoked, err := s.queries.DeleteUserSession(ctx, db.DeleteUserSessionParams{PublicID: input.ID, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The session does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "revoke session", err)
	}
	output := &revokeSessionOutput{}
	if current, _ := ctx.Value(sessionKey{}).([]byte); bytes.Equal(revoked, current) {
		cookie := s.clearedCookie()
		output.SetCookie = cookie.String()
	}
	return output, nil
}

func (s *Service) revokeOtherSessions(ctx context.Context, _ *struct{}) (*struct{}, error) {
	user, _ := UserFromContext(ctx)
	current, _ := ctx.Value(sessionKey{}).([]byte)
	err := s.queries.DeleteOtherUserSessions(ctx, db.DeleteOtherUserSessionsParams{UserID: user.ID, CurrentTokenHash: current})
	if err != nil {
		return nil, s.internalError(ctx, "revoke other sessions", err)
	}
	return &struct{}{}, nil
}
