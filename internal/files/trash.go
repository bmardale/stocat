package files

import (
	"context"
	"errors"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/replication"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

const (
	trashPageSize = 100
	trashLifetime = 30 * 24 * time.Hour
)

type trashListInput struct {
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
}

type TrashedFile struct {
	ID             string    `json:"id"`
	LibraryID      string    `json:"library_id"`
	LibraryName    string    `json:"library_name"`
	ParentID       string    `json:"parent_id"`
	Name           string    `json:"name,omitempty"`
	EncryptedName  []byte    `json:"encrypted_name,omitempty"`
	NameToken      []byte    `json:"name_token,omitempty"`
	Revision       int64     `json:"revision"`
	TrashedAt      time.Time `json:"trashed_at"`
	DeleteAfter    time.Time `json:"delete_after"`
	EncryptionMode string    `json:"encryption_mode" enum:"none,e2ee"`
}

type trashPage struct {
	Items      []TrashedFile `json:"items" nullable:"false"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type trashOutput struct{ Body trashPage }

func (s *Service) listTrash(ctx context.Context, input *trashListInput) (*trashOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	afterID := int64(0)
	if input.Cursor != "" {
		var err error
		afterID, err = s.queries.GetTrashedFileCursorByPublicIDAndOwner(ctx, db.GetTrashedFileCursorByPublicIDAndOwnerParams{
			NodePublicID: input.Cursor, OwnerID: user.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error422UnprocessableEntity("The trash cursor is not valid.")
		}
		if err != nil {
			return nil, s.internalError(ctx, "load trash cursor", err)
		}
	}
	limit := input.Limit
	if limit == 0 {
		limit = trashPageSize
	}
	rows, err := s.queries.ListTrashedFilesByOwner(ctx, db.ListTrashedFilesByOwnerParams{
		OwnerID: user.ID, AfterID: afterID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, s.internalError(ctx, "list trash", err)
	}
	output := &trashOutput{Body: trashPage{Items: make([]TrashedFile, 0, min(len(rows), int(limit)))}}
	if len(rows) > int(limit) {
		output.Body.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		item := TrashedFile{
			ID: row.PublicID, LibraryID: row.LibraryPublicID, LibraryName: row.LibraryName,
			ParentID: row.ParentPublicID, EncryptedName: row.EncryptedName, NameToken: row.NameToken,
			Revision: row.Revision, TrashedAt: row.TrashedAt.Time,
			DeleteAfter: row.TrashedAt.Time.Add(trashLifetime), EncryptionMode: row.EncryptionMode,
		}
		if row.Name.Valid {
			item.Name = row.Name.String
		}
		output.Body.Items = append(output.Body.Items, item)
	}
	return output, nil
}

func (s *Service) restore(ctx context.Context, input *fileInput) (*nodeOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	file, err := s.queries.GetTrashedFileNodeByPublicIDAndOwner(ctx, db.GetTrashedFileNodeByPublicIDAndOwnerParams{
		NodePublicID: input.ID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fileNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "load trashed file", err)
	}
	if err := replication.EnsureLibraryWritable(ctx, s.queries, file.LibraryPublicID, user.ID); err != nil {
		return nil, err
	}
	var row db.Node
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		if row, err = queries.RestoreFileNode(ctx, file.ID); err != nil {
			return err
		}
		return recordFileEvent(ctx, queries, audit.FileRestored, file.ID, user.ID, nil)
	})
	if isPgError(err, uniqueViolation) {
		return nil, huma.Error409Conflict("A file or folder with this name already exists.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fileNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "restore file", err)
	}
	return &nodeOutput{Body: libraries.NodeFromRow(row, file.LibraryPublicID, file.ParentPublicID)}, nil
}
