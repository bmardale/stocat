// Package trash lists, restores, and permanently deletes trashed nodes.
package trash

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Retention is the time a trashed node stays before the purge worker deletes it.
const Retention = 30 * 24 * time.Hour

const uniqueViolation = "23505"

const pageSize = 100

type TrashItem struct {
	ID             string    `json:"id"`
	LibraryID      string    `json:"library_id"`
	LibraryName    string    `json:"library_name"`
	EncryptionMode string    `json:"encryption_mode" enum:"none,e2ee"`
	ParentID       string    `json:"parent_id"`
	Kind           string    `json:"kind" enum:"file,folder"`
	Name           string    `json:"name,omitempty"`
	EncryptedName  []byte    `json:"encrypted_name,omitempty"`
	NameToken      []byte    `json:"name_token,omitempty"`
	Revision       int64     `json:"revision"`
	TrashedAt      time.Time `json:"trashed_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	queue   *river.Client[pgx.Tx]
	log     *slog.Logger
}

type listInput struct {
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
}

type nodeInput struct {
	ID string `path:"id" maxLength:"64"`
}

type trashPage struct {
	Items      []TrashItem `json:"items" nullable:"false"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type trashPageOutput struct{ Body trashPage }

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), log: logger}
}

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/trash")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Trash"} })
	huma.Register(group, huma.Operation{
		OperationID: "trash-list", Method: http.MethodGet, Path: "", Summary: "List trashed nodes",
		Errors: []int{http.StatusUnprocessableEntity},
	}, s.list)
	huma.Register(group, huma.Operation{
		OperationID: "trash-empty", Method: http.MethodDelete, Path: "", Summary: "Permanently delete all trashed nodes",
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusConflict, http.StatusServiceUnavailable},
	}, s.empty)
	huma.Register(group, huma.Operation{
		OperationID: "trash-restore", Method: http.MethodPost, Path: "/{id}/restore", Summary: "Restore a trashed node",
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusConflict, http.StatusNotFound},
	}, s.restore)
	huma.Register(group, huma.Operation{
		OperationID: "trash-delete", Method: http.MethodDelete, Path: "/{id}", Summary: "Permanently delete a trashed node",
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusConflict, http.StatusNotFound, http.StatusServiceUnavailable},
	}, s.remove)
}

func (s *Service) list(ctx context.Context, input *listInput) (*trashPageOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	afterID := int64(0)
	if input.Cursor != "" {
		cursor, err := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
			PublicID: input.Cursor, OwnerID: user.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !cursor.TrashedAt.Valid {
			return nil, huma.Error422UnprocessableEntity("The cursor is not valid for the trash.")
		}
		if err != nil {
			return nil, s.internalError(ctx, "get trash cursor", err)
		}
		afterID = cursor.ID
	}
	limit := input.Limit
	if limit == 0 {
		limit = pageSize
	}
	rows, err := s.queries.ListTrashRootsByOwner(ctx, db.ListTrashRootsByOwnerParams{
		OwnerID: user.ID, AfterID: afterID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, s.internalError(ctx, "list trash", err)
	}
	output := &trashPageOutput{Body: trashPage{Items: make([]TrashItem, 0, min(len(rows), int(limit)))}}
	if len(rows) > int(limit) {
		output.Body.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		output.Body.Items = append(output.Body.Items, itemFromRow(row))
	}
	return output, nil
}

func (s *Service) restore(ctx context.Context, input *nodeInput) (*struct{}, error) {
	node, err := s.loadNode(ctx, input.ID, true)
	if err != nil {
		return nil, err
	}
	if node.ParentID.Valid {
		parent, loadErr := s.queries.GetNodeByID(ctx, node.ParentID.Int64)
		if loadErr != nil && !errors.Is(loadErr, pgx.ErrNoRows) {
			return nil, s.internalError(ctx, "load parent of trashed node", loadErr)
		}
		if errors.Is(loadErr, pgx.ErrNoRows) || parent.TrashedAt.Valid {
			return nil, huma.Error409Conflict("Restore the parent folder first.")
		}
	}
	if _, err := s.queries.RestoreNodeSubtree(ctx, node.ID); err != nil {
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("A file or folder with this name already exists.")
		}
		return nil, s.internalError(ctx, "restore node", err)
	}
	return nil, nil
}

func (s *Service) remove(ctx context.Context, input *nodeInput) (*struct{}, error) {
	node, err := s.loadNode(ctx, input.ID, true)
	if err != nil {
		return nil, err
	}
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("Permanent deletion is temporarily unavailable.")
	}
	if err := s.purgeNode(ctx, node); err != nil {
		if errors.Is(err, errPurgeBlocked) {
			return nil, huma.Error409Conflict("Cancel the active upload in this folder first.")
		}
		return nil, err
	}
	return nil, nil
}

func (s *Service) empty(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("Permanent deletion is temporarily unavailable.")
	}
	user, _ := auth.UserFromContext(ctx)
	afterID := int64(0)
	for {
		rows, err := s.queries.ListTrashRootsByOwner(ctx, db.ListTrashRootsByOwnerParams{
			OwnerID: user.ID, AfterID: afterID, PageLimit: pageSize,
		})
		if err != nil {
			return nil, s.internalError(ctx, "list trash for emptying", err)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		for _, row := range rows {
			node, err := s.queries.GetNodeByID(ctx, row.ID)
			if err != nil {
				return nil, s.internalError(ctx, "load trashed node", err)
			}
			if err := s.purgeNode(ctx, node); err != nil {
				if errors.Is(err, errPurgeBlocked) {
					return nil, huma.Error409Conflict("Cancel the active upload in this folder first.")
				}
				return nil, err
			}
			afterID = row.ID
		}
	}
}

func (s *Service) loadNode(ctx context.Context, publicID string, trashed bool) (db.Node, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
		PublicID: publicID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.TrashedAt.Valid != trashed {
		return row, huma.Error404NotFound("The trashed item does not exist.")
	}
	if err != nil {
		return row, s.internalError(ctx, "load trashed node", err)
	}
	return row, nil
}

func itemFromRow(row db.ListTrashRootsByOwnerRow) TrashItem {
	item := TrashItem{
		ID: row.PublicID, LibraryID: row.LibraryPublicID, LibraryName: row.LibraryName,
		EncryptionMode: row.LibraryEncryptionMode, Kind: row.Kind, EncryptedName: row.EncryptedName,
		NameToken: row.NameToken, Revision: row.Revision, TrashedAt: row.TrashedAt.Time,
		ExpiresAt: row.TrashedAt.Time.Add(Retention), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.Name.Valid {
		item.Name = row.Name.String
	}
	if row.ParentID.Valid {
		item.ParentID = row.ParentPublicID
	}
	return item
}

func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == uniqueViolation
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("File storage is unavailable.")
}
