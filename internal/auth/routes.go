package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/platform/ratelimit"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type registerInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"200"`
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" writeOnly:"true" doc:"Use at least 15 characters and at most 1024 bytes."`
	}
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" writeOnly:"true"`
	}
}

type userOutput struct {
	Body User
}

type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      User
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

var errInvalidCredentials = errors.New("invalid credentials")

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/api/v1/auth")
	group.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Auth"}
		ratelimit.Document(api, op)
	})
	group.UseMiddleware(captureMetadata)
	group.UseTransformer(apierr.RedactValues)
	huma.Register(group, huma.Operation{
		OperationID: "auth-register", Method: http.MethodPost, Path: "/register",
		Middlewares: huma.Middlewares{s.limitCredentials(api, s.authIP, s.registerIP)},
		Summary:     "Create an account and sign in", DefaultStatus: http.StatusCreated,
		MaxBodyBytes: 8192,
		Errors:       []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, s.register)
	huma.Register(group, huma.Operation{
		OperationID: "auth-login", Method: http.MethodPost, Path: "/login",
		Middlewares:  huma.Middlewares{s.limitCredentials(api, s.authIP)},
		MaxBodyBytes: 8192,
		Summary:      "Sign in", Errors: []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, s.login)
	huma.Register(group, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: "/logout",
		Summary: "Revoke the current session", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusInternalServerError},
	}, s.logout)
	huma.Register(group, huma.Operation{
		OperationID: "auth-passkey-login-options", Method: http.MethodPost, Path: "/passkeys/login/options",
		Middlewares: huma.Middlewares{s.limitCredentials(api, s.authIP)},
		Summary:     "Start a passkey sign-in",
		Description: "Return PublicKeyCredentialRequestOptionsJSON. The options expire after 5 minutes.",
		Errors:      []int{http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, s.passkeyLoginOptions)
	huma.Register(group, huma.Operation{
		OperationID: "auth-passkey-login", Method: http.MethodPost, Path: "/passkeys/login",
		Middlewares:  huma.Middlewares{s.limitCredentials(api, s.authIP)},
		MaxBodyBytes: 16384,
		Summary:      "Sign in with a passkey",
		Description:  "Send the AuthenticationResponseJSON value from PublicKeyCredential.toJSON().",
		Errors: []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusInternalServerError,
			http.StatusServiceUnavailable},
	}, s.passkeyLogin)
	protected := s.Protected(group)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-me", Method: http.MethodGet, Path: "/me",
		Summary: "Get the current user",
	}, func(ctx context.Context, _ *struct{}) (*userOutput, error) {
		user, _ := UserFromContext(ctx)
		return &userOutput{Body: user}, nil
	})
	huma.Register(protected, huma.Operation{
		OperationID: "auth-account-update", Method: http.MethodPatch, Path: "/account",
		Summary: "Update the current account", MaxBodyBytes: 8192,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.updateAccount)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-password-change", Method: http.MethodPut, Path: "/password",
		Summary: "Change the current account password", DefaultStatus: http.StatusNoContent,
		MaxBodyBytes: 4096, Errors: []int{http.StatusUnprocessableEntity},
	}, s.changePassword)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-sessions-list", Method: http.MethodGet, Path: "/sessions",
		Summary: "List the active sessions of the current user",
	}, s.listSessions)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-sessions-revoke-others", Method: http.MethodDelete, Path: "/sessions",
		Summary: "Revoke all sessions except the current session", DefaultStatus: http.StatusNoContent,
	}, s.revokeOtherSessions)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-sessions-revoke", Method: http.MethodDelete, Path: "/sessions/{id}",
		Summary: "Revoke a session", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.revokeSession)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-passkeys-list", Method: http.MethodGet, Path: "/passkeys",
		Summary: "List the passkeys of the current user",
	}, s.listPasskeys)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-passkey-registration-options", Method: http.MethodPost, Path: "/passkeys/registration/options",
		Summary:      "Start a passkey registration",
		Description:  "Return PublicKeyCredentialCreationOptionsJSON. The options expire after 5 minutes.",
		MaxBodyBytes: 4096, Errors: []int{http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, s.passkeyRegistrationOptions)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-passkey-create", Method: http.MethodPost, Path: "/passkeys",
		Summary: "Register a passkey", DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, s.createPasskey)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-passkey-rename", Method: http.MethodPatch, Path: "/passkeys/{id}",
		Summary: "Rename a passkey", MaxBodyBytes: 4096,
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.renamePasskey)
	huma.Register(protected, huma.Operation{
		OperationID: "auth-passkey-delete", Method: http.MethodDelete, Path: "/passkeys/{id}",
		Summary: "Delete a passkey", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.deletePasskey)
}

func (s *Service) register(ctx context.Context, input *registerInput) (*sessionOutput, error) {
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a name.")
	}
	// Validate password length here to keep the password out of validation errors.
	if !validNewPassword(input.Body.Password) {
		return nil, huma.Error422UnprocessableEntity("Use a password with at least 15 characters and at most 1024 bytes.")
	}
	if delay := s.registerEmail.Allow(normalizeEmail(input.Body.Email)); delay > 0 {
		return nil, ratelimit.Error(delay)
	}
	passwordHash := hashPassword(input.Body.Password)
	output := &sessionOutput{}
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		user, err := queries.CreateUser(ctx, db.CreateUserParams{
			PublicID: id.New(id.User), Name: name,
			Email: normalizeEmail(input.Body.Email), PasswordHash: passwordHash,
		})
		if err != nil {
			return err
		}
		output.Body = publicUser(user)
		if output.SetCookie, err = s.createSession(ctx, queries, user.ID); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{Action: audit.AccountRegistered, ActorID: user.ID})
	})
	if err != nil {
		if isEmailConflict(err) {
			return nil, huma.Error409Conflict("An account with this email already exists.")
		}
		return nil, s.internalError(ctx, "register user", err)
	}
	return output, nil
}

func (s *Service) login(ctx context.Context, input *loginInput) (*sessionOutput, error) {
	meta, _ := ctx.Value(metadataKey{}).(requestMetadata)
	remote := ""
	if meta.ip != nil {
		remote = meta.ip.String()
	}
	email := normalizeEmail(input.Body.Email)
	if delay := ratelimit.AllowAll(
		ratelimit.Check{Limiter: s.loginEmailIP, Key: email + "\x00" + ratelimit.IPKey(remote)},
		ratelimit.Check{Limiter: s.loginEmail, Key: email},
	); delay > 0 {
		return nil, ratelimit.Error(delay)
	}
	if input.Body.Password == "" || len(input.Body.Password) > 1024 {
		return nil, huma.Error401Unauthorized("The email or password is incorrect.")
	}
	output := &sessionOutput{}
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		user, err := queries.GetUserByEmailForUpdate(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			// Perform the same password work when the email does not exist.
			_ = passwordKey(input.Body.Password, make([]byte, 16))
			return errInvalidCredentials
		}
		if err != nil {
			return err
		}
		if !verifyPassword(input.Body.Password, user.PasswordHash) {
			return errInvalidCredentials
		}
		output.Body = publicUser(user)
		if output.SetCookie, err = s.createSession(ctx, queries, user.ID); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountSignedIn, ActorID: user.ID, Details: audit.Details{Method: audit.MethodPassword},
		})
	})
	if errors.Is(err, errInvalidCredentials) {
		return nil, huma.Error401Unauthorized("The email or password is incorrect.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "login user", err)
	}
	return output, nil
}

func (s *Service) logout(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
	meta, _ := ctx.Value(metadataKey{}).(requestMetadata)
	if hash := tokenHash(meta.token); hash != nil {
		err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
			session, err := queries.RevokeSession(ctx, hash)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return audit.Record(ctx, queries, audit.Event{
				Action: audit.AccountSignedOut, ActorID: session.UserID, TargetID: session.PublicID,
			})
		})
		if err != nil {
			return nil, s.internalError(ctx, "delete session", err)
		}
	}
	return &logoutOutput{SetCookie: s.clearedCookie()}, nil
}

// The email format validation accepts leading and trailing spaces.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isEmailConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		(pgErr.ConstraintName == "users_email_key" || pgErr.ConstraintName == "users_email_lower_key")
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Authentication is unavailable.")
}
