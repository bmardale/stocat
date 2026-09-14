package files

import (
	"context"
	"errors"
	"net/http"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

type fileTagInput struct {
	ID    string `path:"id" maxLength:"64"`
	TagID string `path:"tag_id" maxLength:"64"`
}

func (s *Service) registerTags(group huma.API) {
	huma.Register(group, huma.Operation{
		OperationID: "files-tags-add", Method: http.MethodPut, Path: "/{id}/tags/{tag_id}",
		Summary: "Add a tag to a file", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.addTag)
	huma.Register(group, huma.Operation{
		OperationID: "files-tags-remove", Method: http.MethodDelete, Path: "/{id}/tags/{tag_id}",
		Summary: "Remove a tag from a file", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.removeTag)
}

func (s *Service) addTag(ctx context.Context, input *fileTagInput) (*struct{}, error) {
	file, tag, err := s.fileAndTag(ctx, input)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFromContext(ctx)
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		added, err := queries.AddFileTag(ctx, db.AddFileTagParams{
			NodeID: file.ID, TagID: tag.ID, LibraryID: file.LibraryID,
		})
		if err != nil || added == 0 {
			return err
		}
		return recordFileEvent(ctx, queries, audit.FileTagged, file.ID, user.ID, &tag)
	})
	if err != nil {
		return nil, s.internalError(ctx, "add file tag", err)
	}
	return nil, nil
}

func (s *Service) removeTag(ctx context.Context, input *fileTagInput) (*struct{}, error) {
	file, tag, err := s.fileAndTag(ctx, input)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFromContext(ctx)
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		removed, err := queries.RemoveFileTag(ctx, db.RemoveFileTagParams{NodeID: file.ID, TagID: tag.ID})
		if err != nil || removed == 0 {
			return err
		}
		return recordFileEvent(ctx, queries, audit.FileUntagged, file.ID, user.ID, &tag)
	})
	if err != nil {
		return nil, s.internalError(ctx, "remove file tag", err)
	}
	return nil, nil
}

func (s *Service) fileAndTag(ctx context.Context, input *fileTagInput) (db.GetFileNodeByPublicIDAndOwnerRow, db.Tag, error) {
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return file, db.Tag{}, err
	}
	user, _ := auth.UserFromContext(ctx)
	tag, err := s.queries.GetTagByPublicIDAndOwner(ctx, db.GetTagByPublicIDAndOwnerParams{
		PublicID: input.TagID, OwnerID: user.ID,
	})
	if err != nil || tag.LibraryID != file.LibraryID {
		if err == nil {
			return file, tag, huma.Error404NotFound("The tag does not exist.")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return file, tag, huma.Error404NotFound("The tag does not exist.")
		}
		return file, tag, s.internalError(ctx, "get file tag", err)
	}
	return file, tag, nil
}
