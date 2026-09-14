// Package admin provides user administration for administrators.
package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bmardale/stocat/internal/db"
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
	pool    *pgxpool.Pool
	queries *db.Queries
	log     *slog.Logger
}

var errBackendNotFound = errors.New("quota backend not found")

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
	return &Service{pool: pool, queries: db.New(pool), log: logger}
}

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
		return nil
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
	stored, err := s.queries.UpdateQuotaSettings(ctx, limit)
	if err != nil {
		return nil, s.internalError(ctx, "set quota settings", err)
	}
	return &quotaSettingsOutput{Body: QuotaSettings{DefaultQuota: quotaFromLimit(true, stored)}}, nil
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
