package files

import (
	"context"
	"errors"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/bmardale/stocat/internal/replication"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type moveInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		LibraryID string `json:"library_id" minLength:"1" maxLength:"64"`
		ParentID  string `json:"parent_id,omitempty" maxLength:"64"`
	}
}

func (s *Service) move(ctx context.Context, input *moveInput) (*nodeOutput, error) {
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFromContext(ctx)
	if err := replication.EnsureLibraryWritable(ctx, s.queries, file.LibraryPublicID, user.ID); err != nil {
		return nil, err
	}
	if err := replication.EnsureLibraryWritable(ctx, s.queries, input.Body.LibraryID, user.ID); err != nil {
		return nil, err
	}
	destination, err := s.queries.GetMoveDestination(ctx, db.GetMoveDestinationParams{
		LibraryPublicID: input.Body.LibraryID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The destination library does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "load move destination", err)
	}
	if destination.EncryptionMode != file.EncryptionMode {
		return nil, huma.Error409Conflict("Move files only between libraries with the same encryption mode.")
	}
	source, err := s.queries.GetMoveDestination(ctx, db.GetMoveDestinationParams{
		LibraryPublicID: file.LibraryPublicID, OwnerID: user.ID,
	})
	if err != nil {
		return nil, s.internalError(ctx, "load move source", err)
	}
	if destination.BackendID != source.BackendID {
		return nil, huma.Error409Conflict("Move files only between libraries on the same storage backend.")
	}
	parentID, parentPublicID := destination.RootNodeID, destination.RootNodePublicID
	if input.Body.ParentID != "" {
		folder, loadErr := s.queries.GetMoveFolder(ctx, db.GetMoveFolderParams{
			NodePublicID: input.Body.ParentID, OwnerID: user.ID, LibraryID: destination.ID,
		})
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("The destination folder does not exist.")
		}
		if loadErr != nil {
			return nil, s.internalError(ctx, "load move folder", loadErr)
		}
		parentID, parentPublicID = folder.ID, folder.PublicID
	}
	var row db.Node
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		event, err := audit.FileEvent(ctx, queries, audit.FileMoved, file.ID)
		if err != nil {
			return err
		}
		row, err = queries.MoveFileNode(ctx, db.MoveFileNodeParams{
			NodeID: file.ID, DestinationLibraryID: destination.ID,
			DestinationParentID: pgtype.Int8{Int64: parentID, Valid: true},
		})
		if err != nil {
			return err
		}
		event.ActorID = user.ID
		event.Details.DestinationLibraryID = destination.PublicID
		event.Details.DestinationLibraryName = destination.Name.String
		return audit.Record(ctx, queries, event)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error409Conflict("A shared file cannot move between libraries.")
	}
	if isPgError(err, uniqueViolation) {
		return nil, huma.Error409Conflict("A file or folder with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "move file", err)
	}
	return &nodeOutput{Body: libraries.NodeFromRow(row, destination.PublicID, parentPublicID)}, nil
}
