// Package admin provides user administration for administrators.
package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bmardale/stocat/internal/accountdeletion"
	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ModeInherit   = "inherit"
	ModeUnlimited = "unlimited"
	ModeLimited   = "limited"
)

type Service struct {
	pool     *pgxpool.Pool
	queries  *db.Queries
	log      *slog.Logger
	deletion *accountdeletion.Service
}

var (
	errBackendNotFound = errors.New("quota backend not found")
	errLastAdmin       = errors.New("last administrator cannot be removed")
	errSelfRoleChange  = errors.New("administrator cannot change own role")
	errSelfDeletion    = errors.New("administrator cannot delete own account")
)

type Quota struct {
	Mode       string `json:"mode" enum:"inherit,unlimited,limited"`
	LimitBytes *int64 `json:"limit_bytes,omitempty" minimum:"0" doc:"Send the limit only with the limited mode."`
}

type BackendQuota struct {
	BackendID  string `json:"backend_id" maxLength:"64"`
	Mode       string `json:"mode" enum:"inherit,unlimited,limited"`
	LimitBytes *int64 `json:"limit_bytes,omitempty" minimum:"0" doc:"Send the limit only with the limited mode."`
}

type AdminUser struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Email         string         `json:"email"`
	IsAdmin       bool           `json:"is_admin"`
	DefaultQuota  Quota          `json:"default_quota" doc:"The inherit mode uses the global default quota."`
	BackendQuotas []BackendQuota `json:"backend_quotas" nullable:"false" doc:"A backend without an override uses the default quota of the user."`
}

type QuotaSettings struct {
	DefaultQuota Quota `json:"default_quota" doc:"The global default quota. The inherit mode is not allowed."`
}

type updateUserQuotaInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		DefaultQuota  Quota          `json:"default_quota" doc:"The inherit mode uses the global default quota."`
		BackendQuotas []BackendQuota `json:"backend_quotas" maxItems:"1000" nullable:"false" doc:"The list replaces all backend overrides. The inherit mode removes an override."`
	}
}

type createUserInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"200"`
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" writeOnly:"true" doc:"Use at least 15 characters and at most 1024 bytes."`
		IsAdmin  bool   `json:"is_admin"`
	}
}

type updateUserInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"200"`
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password,omitempty" writeOnly:"true" doc:"Leave empty to keep the current password. Use at least 15 characters and at most 1024 bytes."`
		IsAdmin  bool   `json:"is_admin"`
	}
}

type userPathInput struct {
	ID string `path:"id" maxLength:"64"`
}

type updateQuotaSettingsInput struct {
	Body QuotaSettings
}

type userOutput struct{ Body AdminUser }

type usersOutput struct {
	Body []AdminUser `nullable:"false"`
}

type quotaSettingsOutput struct{ Body QuotaSettings }

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), log: logger, deletion: accountdeletion.New(pool, logger)}
}

func (s *Service) UseUserDeletion(deletion *accountdeletion.Service) { s.deletion = deletion }

// Register adds the routes to api. The caller must restrict api to administrators.
func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/users")
	group.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Admin"}
	})
	huma.Register(group, huma.Operation{
		OperationID: "admin-users-list", Method: http.MethodGet, Path: "",
		Summary: "List users with their quotas",
	}, s.list)
	huma.Register(group, huma.Operation{
		OperationID: "admin-users-create", Method: http.MethodPost, Path: "",
		Summary: "Create a user", DefaultStatus: http.StatusCreated, MaxBodyBytes: 8192,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.createUser)
	huma.Register(group, huma.Operation{
		OperationID: "admin-users-update", Method: http.MethodPatch, Path: "/{id}",
		Summary: "Update a user", MaxBodyBytes: 8192,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.updateUser)
	huma.Register(group, huma.Operation{
		OperationID: "admin-users-delete", Method: http.MethodDelete, Path: "/{id}",
		Summary: "Delete a user", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusServiceUnavailable},
	}, s.deleteUser)
	huma.Register(group, huma.Operation{
		OperationID: "admin-users-quota-set", Method: http.MethodPut, Path: "/{id}/quota",
		Summary: "Set the quotas of a user", MaxBodyBytes: 131072,
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.setUserQuota)

	settings := huma.NewGroup(api, "/quota")
	settings.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Admin"}
	})
	huma.Register(settings, huma.Operation{
		OperationID: "admin-quota-get", Method: http.MethodGet, Path: "",
		Summary: "Get the global quota settings",
	}, s.getQuotaSettings)
	huma.Register(settings, huma.Operation{
		OperationID: "admin-quota-set", Method: http.MethodPut, Path: "",
		Summary: "Set the global quota settings", MaxBodyBytes: 4096,
		Errors: []int{http.StatusUnprocessableEntity},
	}, s.setQuotaSettings)
	s.registerRegistration(api)
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*usersOutput, error) {
	users, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list users", err)
	}
	defaults, err := s.queries.ListUserDefaultQuotas(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list user default quotas", err)
	}
	overrides, err := s.queries.ListUserBackendQuotas(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list user backend quotas", err)
	}
	defaultByUser := make(map[int64]Quota, len(defaults))
	for _, row := range defaults {
		defaultByUser[row.UserID] = quotaFromLimit(true, row.LimitBytes)
	}
	overridesByUser := make(map[int64][]BackendQuota, len(users))
	for _, row := range overrides {
		quota := quotaFromLimit(true, row.LimitBytes)
		overridesByUser[row.UserID] = append(overridesByUser[row.UserID], BackendQuota{
			BackendID: row.BackendPublicID, Mode: quota.Mode, LimitBytes: quota.LimitBytes,
		})
	}
	output := &usersOutput{Body: make([]AdminUser, 0, len(users))}
	for _, user := range users {
		defaultQuota, ok := defaultByUser[user.ID]
		if !ok {
			defaultQuota = Quota{Mode: ModeInherit}
		}
		backendQuotas := overridesByUser[user.ID]
		if backendQuotas == nil {
			backendQuotas = []BackendQuota{}
		}
		output.Body = append(output.Body, AdminUser{
			ID: user.PublicID, Name: user.Name, Email: user.Email, IsAdmin: user.IsAdmin,
			DefaultQuota: defaultQuota, BackendQuotas: backendQuotas,
		})
	}
	return output, nil
}

func (s *Service) createUser(ctx context.Context, input *createUserInput) (*userOutput, error) {
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a name.")
	}
	passwordHash, valid := auth.NewPasswordHash(input.Body.Password)
	if !valid {
		return nil, huma.Error422UnprocessableEntity("Use a password with at least 15 characters and at most 1024 bytes.")
	}
	admin, _ := auth.UserFromContext(ctx)
	var user db.User
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		user, err = queries.CreateUser(ctx, db.CreateUserParams{
			PublicID: id.New(id.User), Name: name, Email: normalizeEmail(input.Body.Email), PasswordHash: passwordHash,
		})
		if err != nil {
			return err
		}
		if input.Body.IsAdmin {
			user, err = queries.UpdateAdminUser(ctx, db.UpdateAdminUserParams{
				ID: user.ID, Name: user.Name, Email: user.Email, IsAdmin: true,
			})
			if err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountRegistered, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID,
		}); err != nil {
			return err
		}
		if !input.Body.IsAdmin {
			return nil
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountAdminGranted, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID,
		})
	})
	if auth.IsEmailConflict(err) {
		return nil, huma.Error409Conflict("An account with this email already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create user", err)
	}
	managed, err := s.adminUser(ctx, user)
	if err != nil {
		return nil, s.internalError(ctx, "load created user", err)
	}
	return &userOutput{Body: managed}, nil
}

func (s *Service) updateUser(ctx context.Context, input *updateUserInput) (*userOutput, error) {
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a name.")
	}
	var passwordHash string
	if input.Body.Password != "" {
		var valid bool
		passwordHash, valid = auth.NewPasswordHash(input.Body.Password)
		if !valid {
			return nil, huma.Error422UnprocessableEntity("Use a password with at least 15 characters and at most 1024 bytes.")
		}
	}
	admin, _ := auth.UserFromContext(ctx)
	currentSession := auth.SessionTokenHash(ctx)
	var user db.User
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		target, err := queries.GetUserByPublicIDForUpdate(ctx, input.ID)
		if err != nil {
			return err
		}
		if target.ID == admin.ID && target.IsAdmin != input.Body.IsAdmin {
			return errSelfRoleChange
		}
		if target.IsAdmin && !input.Body.IsAdmin {
			administrators, err := queries.LockAdministrators(ctx)
			if err != nil {
				return err
			}
			if len(administrators) <= 1 {
				return errLastAdmin
			}
		}
		user, err = queries.UpdateAdminUser(ctx, db.UpdateAdminUserParams{
			ID: target.ID, Name: name, Email: normalizeEmail(input.Body.Email), IsAdmin: input.Body.IsAdmin,
		})
		if err != nil {
			return err
		}
		var details audit.Details
		if user.Name != target.Name {
			details.Name, details.PreviousName = user.Name, target.Name
		}
		if user.Email != target.Email {
			details.Email, details.PreviousEmail = user.Email, target.Email
		}
		if details != (audit.Details{}) {
			if err := audit.Record(ctx, queries, audit.Event{
				Action: audit.AccountUpdated, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID,
				Details: details,
			}); err != nil {
				return err
			}
		}
		if user.IsAdmin != target.IsAdmin {
			action := audit.AccountAdminRevoked
			if user.IsAdmin {
				action = audit.AccountAdminGranted
			}
			if err := audit.Record(ctx, queries, audit.Event{
				Action: action, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID,
			}); err != nil {
				return err
			}
		}
		if passwordHash == "" {
			return nil
		}
		if err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: user.ID, PasswordHash: passwordHash}); err != nil {
			return err
		}
		if user.ID == admin.ID && len(currentSession) > 0 {
			if err := queries.DeleteOtherUserSessions(ctx, db.DeleteOtherUserSessionsParams{
				UserID: user.ID, CurrentTokenHash: currentSession,
			}); err != nil {
				return err
			}
		} else if err := queries.DeleteAllUserSessions(ctx, user.ID); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountPasswordChanged, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID,
		})
	})
	if errors.Is(err, errSelfRoleChange) {
		return nil, huma.Error409Conflict("You cannot change your own administrator role.")
	}
	if errors.Is(err, errLastAdmin) {
		return nil, huma.Error409Conflict("Keep at least one administrator account.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	}
	if auth.IsEmailConflict(err) {
		return nil, huma.Error409Conflict("An account with this email already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "update user", err)
	}
	managed, err := s.adminUser(ctx, user)
	if err != nil {
		return nil, s.internalError(ctx, "load updated user", err)
	}
	return &userOutput{Body: managed}, nil
}

func (s *Service) deleteUser(ctx context.Context, input *userPathInput) (*struct{}, error) {
	admin, _ := auth.UserFromContext(ctx)
	target, err := s.queries.GetUserByPublicID(ctx, input.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "load user for deletion", err)
	}
	err = s.deletion.DeleteUserWithCheck(ctx, target.ID, admin.ID, func(ctx context.Context, queries *db.Queries, user db.User) error {
		if user.ID == admin.ID {
			return errSelfDeletion
		}
		if !user.IsAdmin {
			return nil
		}
		administrators, err := queries.LockAdministrators(ctx)
		if err != nil {
			return err
		}
		if len(administrators) <= 1 {
			return errLastAdmin
		}
		return nil
	})
	if errors.Is(err, errSelfDeletion) {
		return nil, huma.Error409Conflict("You cannot delete your own administrator account here.")
	}
	if errors.Is(err, errLastAdmin) {
		return nil, huma.Error409Conflict("Keep at least one administrator account.")
	}
	if errors.Is(err, accountdeletion.ErrQueueUnavailable) {
		return nil, huma.Error503ServiceUnavailable("Account deletion is temporarily unavailable.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete user", err)
	}
	return &struct{}{}, nil
}

func (s *Service) adminUser(ctx context.Context, user db.User) (AdminUser, error) {
	defaults, err := s.queries.ListUserDefaultQuotas(ctx)
	if err != nil {
		return AdminUser{}, err
	}
	defaultQuota := Quota{Mode: ModeInherit}
	for _, row := range defaults {
		if row.UserID == user.ID {
			defaultQuota = quotaFromLimit(true, row.LimitBytes)
			break
		}
	}
	overrides, err := s.queries.ListUserBackendQuotas(ctx)
	if err != nil {
		return AdminUser{}, err
	}
	backendQuotas := make([]BackendQuota, 0)
	for _, row := range overrides {
		if row.UserID != user.ID {
			continue
		}
		quota := quotaFromLimit(true, row.LimitBytes)
		backendQuotas = append(backendQuotas, BackendQuota{
			BackendID: row.BackendPublicID, Mode: quota.Mode, LimitBytes: quota.LimitBytes,
		})
	}
	return AdminUser{
		ID: user.PublicID, Name: user.Name, Email: user.Email, IsAdmin: user.IsAdmin,
		DefaultQuota: defaultQuota, BackendQuotas: backendQuotas,
	}, nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (s *Service) setUserQuota(ctx context.Context, input *updateUserQuotaInput) (*userOutput, error) {
	defaultSet, defaultLimit, err := quotaLevel(input.Body.DefaultQuota)
	if err != nil {
		return nil, err
	}
	overrides := make([]BackendQuota, 0, len(input.Body.BackendQuotas))
	seen := make(map[string]bool, len(input.Body.BackendQuotas))
	for _, override := range input.Body.BackendQuotas {
		if seen[override.BackendID] {
			return nil, huma.Error422UnprocessableEntity("Send each storage backend only once.")
		}
		seen[override.BackendID] = true
		if _, _, err := quotaLevel(Quota{Mode: override.Mode, LimitBytes: override.LimitBytes}); err != nil {
			return nil, err
		}
		if override.Mode != ModeInherit {
			overrides = append(overrides, override)
		}
	}
	var user db.User
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		user, err = queries.GetUserByPublicIDForUpdate(ctx, input.ID)
		if err != nil {
			return err
		}
		if err := queries.DeleteUserDefaultQuota(ctx, user.ID); err != nil {
			return err
		}
		if defaultSet {
			if err := queries.CreateUserDefaultQuota(ctx, db.CreateUserDefaultQuotaParams{
				UserID: user.ID, LimitBytes: defaultLimit,
			}); err != nil {
				return err
			}
		}
		if err := queries.DeleteUserBackendQuotas(ctx, user.ID); err != nil {
			return err
		}
		for _, override := range overrides {
			_, limit, _ := quotaLevel(Quota{Mode: override.Mode, LimitBytes: override.LimitBytes})
			created, err := queries.CreateUserBackendQuota(ctx, db.CreateUserBackendQuotaParams{
				UserID: user.ID, BackendPublicID: override.BackendID, LimitBytes: limit,
			})
			if err != nil {
				return err
			}
			if created == 0 {
				return errBackendNotFound
			}
		}
		admin, _ := auth.UserFromContext(ctx)
		details := quotaDetails(input.Body.DefaultQuota)
		details.BackendQuotaCount = len(overrides)
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.UserQuotaUpdated, ActorID: admin.ID, SubjectID: user.ID, TargetID: user.PublicID, Details: details,
		})
	})
	if errors.Is(err, errBackendNotFound) {
		return nil, huma.Error404NotFound("The storage backend does not exist.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "set user quota", err)
	}
	return &userOutput{Body: AdminUser{
		ID: user.PublicID, Name: user.Name, Email: user.Email, IsAdmin: user.IsAdmin,
		DefaultQuota: input.Body.DefaultQuota, BackendQuotas: overrides,
	}}, nil
}

func (s *Service) getQuotaSettings(ctx context.Context, _ *struct{}) (*quotaSettingsOutput, error) {
	limit, err := s.queries.GetQuotaSettings(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "get quota settings", err)
	}
	return &quotaSettingsOutput{Body: QuotaSettings{DefaultQuota: quotaFromLimit(true, limit)}}, nil
}

func (s *Service) setQuotaSettings(ctx context.Context, input *updateQuotaSettingsInput) (*quotaSettingsOutput, error) {
	if input.Body.DefaultQuota.Mode == ModeInherit {
		return nil, huma.Error422UnprocessableEntity("Select a limit or no limit for the global default quota.")
	}
	_, limit, err := quotaLevel(input.Body.DefaultQuota)
	if err != nil {
		return nil, err
	}
	var stored pgtype.Int8
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		if stored, err = queries.UpdateQuotaSettings(ctx, limit); err != nil {
			return err
		}
		admin, _ := auth.UserFromContext(ctx)
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.DefaultQuotaUpdated, ActorID: admin.ID, Details: quotaDetails(input.Body.DefaultQuota),
		})
	})
	if err != nil {
		return nil, s.internalError(ctx, "set quota settings", err)
	}
	return &quotaSettingsOutput{Body: QuotaSettings{DefaultQuota: quotaFromLimit(true, stored)}}, nil
}

func quotaDetails(quota Quota) audit.Details {
	return audit.Details{QuotaMode: quota.Mode, QuotaLimitBytes: quota.LimitBytes}
}

func quotaFromLimit(set bool, limit pgtype.Int8) Quota {
	switch {
	case !set:
		return Quota{Mode: ModeInherit}
	case !limit.Valid:
		return Quota{Mode: ModeUnlimited}
	default:
		value := limit.Int64
		return Quota{Mode: ModeLimited, LimitBytes: &value}
	}
}

// quotaLevel reports whether quota sets a level and returns its limit. An invalid limit means no limit.
func quotaLevel(quota Quota) (bool, pgtype.Int8, error) {
	switch quota.Mode {
	case ModeLimited:
		if quota.LimitBytes == nil {
			return false, pgtype.Int8{}, huma.Error422UnprocessableEntity("Send limit_bytes with the limited mode.")
		}
		return true, pgtype.Int8{Int64: *quota.LimitBytes, Valid: true}, nil
	case ModeUnlimited, ModeInherit:
		if quota.LimitBytes != nil {
			return false, pgtype.Int8{}, huma.Error422UnprocessableEntity("Send limit_bytes only with the limited mode.")
		}
		return quota.Mode == ModeUnlimited, pgtype.Int8{}, nil
	default:
		return false, pgtype.Int8{}, huma.Error422UnprocessableEntity("Use a supported quota mode.")
	}
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("User administration is unavailable.")
}
