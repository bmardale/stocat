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

type Service struct {
	queries *db.Queries
	log     *slog.Logger
}

type LibraryQuota struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	QuotaMB *int64 `json:"quota_mb" doc:"Quota in megabytes. A null value uses the user default quota."`
}

type AdminUser struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	IsAdmin        bool           `json:"is_admin"`
	DefaultQuotaMB *int64         `json:"default_quota_mb" doc:"Default quota in megabytes. A null value means no limit."`
	Libraries      []LibraryQuota `json:"libraries" nullable:"false"`
}

type updateUserQuotaInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		DefaultQuotaMB *int64 `json:"default_quota_mb" minimum:"0" doc:"Default quota in megabytes. Send null for no limit."`
	}
}

type updateLibraryQuotaInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		QuotaMB *int64 `json:"quota_mb" minimum:"0" doc:"Quota in megabytes. Send null to use the user default quota."`
	}
}

type userOutput struct{ Body AdminUser }

type usersOutput struct {
	Body []AdminUser `nullable:"false"`
}

type libraryQuotaOutput struct{ Body LibraryQuota }

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{queries: db.New(pool), log: logger}
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
		Summary: "Set the default quota of a user", MaxBodyBytes: 4096,
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.setUserQuota)

	libraries := huma.NewGroup(api, "/libraries")
	libraries.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Admin"}
	})
	huma.Register(libraries, huma.Operation{
		OperationID: "admin-libraries-quota-set", Method: http.MethodPut, Path: "/{id}/quota",
		Summary: "Set the quota override of a library", MaxBodyBytes: 4096,
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.setLibraryQuota)
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*usersOutput, error) {
	users, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list users", err)
	}
	libraries, err := s.queries.ListLibrariesForAdmin(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list libraries", err)
	}
	byOwner := make(map[int64][]LibraryQuota, len(users))
	for _, library := range libraries {
		byOwner[library.OwnerID] = append(byOwner[library.OwnerID], LibraryQuota{
			ID: library.PublicID, Name: library.Name, QuotaMB: quotaPtr(library.QuotaMb),
		})
	}
	output := &usersOutput{Body: make([]AdminUser, 0, len(users))}
	for _, user := range users {
		owned := byOwner[user.ID]
		if owned == nil {
			owned = []LibraryQuota{}
		}
		output.Body = append(output.Body, AdminUser{
			ID: user.PublicID, Name: user.Name, Email: user.Email, IsAdmin: user.IsAdmin,
			DefaultQuotaMB: quotaPtr(user.DefaultQuotaMb), Libraries: owned,
		})
	}
	return output, nil
}

func (s *Service) setUserQuota(ctx context.Context, input *updateUserQuotaInput) (*userOutput, error) {
	if _, err := s.queries.UpdateUserDefaultQuota(ctx, db.UpdateUserDefaultQuotaParams{
		PublicID: input.ID, DefaultQuotaMb: quotaValue(input.Body.DefaultQuotaMB),
	}); errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	} else if err != nil {
		return nil, s.internalError(ctx, "set user quota", err)
	}
	return s.user(ctx, input.ID)
}

func (s *Service) setLibraryQuota(ctx context.Context, input *updateLibraryQuotaInput) (*libraryQuotaOutput, error) {
	row, err := s.queries.UpdateLibraryQuota(ctx, db.UpdateLibraryQuotaParams{
		PublicID: input.ID, QuotaMb: quotaValue(input.Body.QuotaMB),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The library does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "set library quota", err)
	}
	return &libraryQuotaOutput{Body: LibraryQuota{
		ID: row.PublicID, Name: row.Name, QuotaMB: quotaPtr(row.QuotaMb),
	}}, nil
}

func (s *Service) user(ctx context.Context, publicID string) (*userOutput, error) {
	user, err := s.queries.GetUserByPublicID(ctx, publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The user does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "get user", err)
	}
	libraries, err := s.queries.ListLibrariesByOwnerID(ctx, user.ID)
	if err != nil {
		return nil, s.internalError(ctx, "list user libraries", err)
	}
	owned := make([]LibraryQuota, 0, len(libraries))
	for _, library := range libraries {
		owned = append(owned, LibraryQuota{
			ID: library.PublicID, Name: library.Name, QuotaMB: quotaPtr(library.QuotaMb),
		})
	}
	return &userOutput{Body: AdminUser{
		ID: user.PublicID, Name: user.Name, Email: user.Email, IsAdmin: user.IsAdmin,
		DefaultQuotaMB: quotaPtr(user.DefaultQuotaMb), Libraries: owned,
	}}, nil
}

func quotaPtr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	quota := value.Int64
	return &quota
}

func quotaValue(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("User administration is unavailable.")
}
