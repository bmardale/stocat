package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
)

var (
	errCurrentPassword   = errors.New("current password is incorrect")
	errPasswordUnchanged = errors.New("new password matches current password")
)

type updateAccountInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"200"`
		Email string `json:"email" format:"email" maxLength:"254"`
	}
}

type changePasswordInput struct {
	Body struct {
		CurrentPassword string `json:"current_password" writeOnly:"true"`
		NewPassword     string `json:"new_password" writeOnly:"true" doc:"Use at least 15 characters and at most 1024 bytes."`
	}
}

func (s *Service) updateAccount(ctx context.Context, input *updateAccountInput) (*userOutput, error) {
	current, _ := UserFromContext(ctx)
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a name.")
	}
	user, err := s.queries.UpdateUserAccount(ctx, db.UpdateUserAccountParams{
		ID: current.ID, Name: name, Email: normalizeEmail(input.Body.Email),
	})
	if isEmailConflict(err) {
		return nil, huma.Error409Conflict("An account with this email already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "update account", err)
	}
	return &userOutput{Body: publicUser(user)}, nil
}

func (s *Service) changePassword(ctx context.Context, input *changePasswordInput) (*struct{}, error) {
	if input.Body.CurrentPassword == "" || len(input.Body.CurrentPassword) > 1024 {
		return nil, huma.Error422UnprocessableEntity("The current password is incorrect.")
	}
	if !validNewPassword(input.Body.NewPassword) {
		return nil, huma.Error422UnprocessableEntity("Use a password with at least 15 characters and at most 1024 bytes.")
	}
	current, _ := UserFromContext(ctx)
	currentSession, _ := ctx.Value(sessionKey{}).([]byte)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		user, err := queries.GetUserByIDForUpdate(ctx, current.ID)
		if err != nil {
			return err
		}
		if !verifyPassword(input.Body.CurrentPassword, user.PasswordHash) {
			return errCurrentPassword
		}
		if verifyPassword(input.Body.NewPassword, user.PasswordHash) {
			return errPasswordUnchanged
		}
		if err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
			ID: current.ID, PasswordHash: hashPassword(input.Body.NewPassword),
		}); err != nil {
			return err
		}
		return queries.DeleteOtherUserSessions(ctx, db.DeleteOtherUserSessionsParams{
			UserID: current.ID, CurrentTokenHash: currentSession,
		})
	})
	if errors.Is(err, errCurrentPassword) {
		return nil, huma.Error422UnprocessableEntity("The current password is incorrect.")
	}
	if errors.Is(err, errPasswordUnchanged) {
		return nil, huma.Error422UnprocessableEntity("Choose a password that differs from the current password.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "change password", err)
	}
	return &struct{}{}, nil
}
