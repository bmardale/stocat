// Package quota resolves storage quotas and reports storage usage.
package quota

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrExceeded = errors.New("storage quota exceeded")

// Level is one quota level. Set is false when the level uses the next level. An invalid Limit means no limit.
type Level struct {
	Set   bool
	Limit pgtype.Int8
}

// Resolve returns the limit for a user on a backend. An invalid result means no limit.
func Resolve(global pgtype.Int8, user, backend Level) pgtype.Int8 {
	if backend.Set {
		return backend.Limit
	}
	if user.Set {
		return user.Limit
	}
	return global
}

// Admit returns ErrExceeded when size does not fit in the quota of the user on the backend.
// Call Admit in the transaction that reserves the upload. The caller must serialize admissions.
func Admit(ctx context.Context, q *db.Queries, userID, backendID, size int64) error {
	row, err := q.GetUserBackendQuota(ctx, db.GetUserBackendQuotaParams{UserID: userID, BackendID: backendID})
	if err != nil {
		return fmt.Errorf("load quota of user %d on backend %d: %w", userID, backendID, err)
	}
	limit := Resolve(row.GlobalLimitBytes,
		Level{Set: row.HasUserDefault, Limit: row.UserLimitBytes},
		Level{Set: row.HasBackendOverride, Limit: row.BackendLimitBytes})
	if limit.Valid && exceeds(row.StoredBytes, row.ReservedBytes, size, limit.Int64) {
		return ErrExceeded
	}
	return nil
}

func exceeds(stored, reserved, size, limit int64) bool {
	return size > limit || stored > limit-size || reserved > limit-size-stored
}

type Usage struct {
	Backend    libraries.LibraryBackend `json:"backend"`
	UsedBytes  int64                    `json:"used_bytes" doc:"Stored bytes of all file versions and trashed files, plus bytes of unfinished uploads."`
	LimitBytes *int64                   `json:"limit_bytes" doc:"A null value means no limit."`
}

type usageOutput struct {
	Body []Usage `nullable:"false"`
}

type Service struct {
	queries *db.Queries
	log     *slog.Logger
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{queries: db.New(pool), log: logger}
}

func (s *Service) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "storage-usage-list", Method: http.MethodGet, Path: "/storage-usage",
		Summary:     "List storage usage and quotas",
		Description: "The list contains each storage backend that a library of the current user uses.",
		Tags:        []string{"Quota"},
	}, s.list)
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*usageOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	rows, err := s.queries.ListUserStorageUsage(ctx, user.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "list storage usage", "error", err)
		return nil, huma.Error500InternalServerError("Storage usage is unavailable.")
	}
	output := &usageOutput{Body: make([]Usage, 0, len(rows))}
	for _, row := range rows {
		usage := Usage{
			Backend:   libraries.LibraryBackend{ID: row.PublicID, Name: row.Name, Type: row.Type},
			UsedBytes: row.StoredBytes + row.ReservedBytes,
		}
		limit := Resolve(row.GlobalLimitBytes,
			Level{Set: row.HasUserDefault, Limit: row.UserLimitBytes},
			Level{Set: row.HasBackendOverride, Limit: row.BackendLimitBytes})
		if limit.Valid {
			usage.LimitBytes = &limit.Int64
		}
		output.Body = append(output.Body, usage)
	}
	return output, nil
}
