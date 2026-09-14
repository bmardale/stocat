package libraries

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var tagColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type Tag struct {
	ID             string    `json:"id"`
	LibraryID      string    `json:"library_id"`
	Name           string    `json:"name,omitempty"`
	EncryptedName  []byte    `json:"encrypted_name,omitempty"`
	NameToken      []byte    `json:"name_token,omitempty"`
	Color          string    `json:"color,omitempty" pattern:"^#[0-9A-F]{6}$"`
	EncryptedColor []byte    `json:"encrypted_color,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type tagBody struct {
	Name           string `json:"name,omitempty" maxLength:"100"`
	EncryptedName  []byte `json:"encrypted_name,omitempty"`
	NameToken      []byte `json:"name_token,omitempty"`
	Color          string `json:"color,omitempty" maxLength:"7"`
	EncryptedColor []byte `json:"encrypted_color,omitempty"`
}

type createTagInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	Body      tagBody
}

type tagInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	TagID     string `path:"tag_id" maxLength:"64"`
}

type updateTagInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	TagID     string `path:"tag_id" maxLength:"64"`
	Body      tagBody
}

type tagsOutput struct {
	Body []Tag `nullable:"false"`
}
type tagOutput struct{ Body Tag }

func (s *Service) registerTags(group huma.API) {
	huma.Register(group, huma.Operation{
		OperationID: "tags-list", Method: http.MethodGet, Path: "/{id}/tags", Summary: "List library tags",
	}, s.listTags)
	huma.Register(group, huma.Operation{
		OperationID: "tags-create", Method: http.MethodPost, Path: "/{id}/tags", Summary: "Create a library tag",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.createTag)
	huma.Register(group, huma.Operation{
		OperationID: "tags-update", Method: http.MethodPatch, Path: "/{id}/tags/{tag_id}", Summary: "Change a library tag",
		MaxBodyBytes: 65536, Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.updateTag)
	huma.Register(group, huma.Operation{
		OperationID: "tags-delete", Method: http.MethodDelete, Path: "/{id}/tags/{tag_id}", Summary: "Delete a library tag",
		DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusNotFound},
	}, s.deleteTag)
}

func (s *Service) listTags(ctx context.Context, input *libraryInput) (*tagsOutput, error) {
	library, err := s.ownedLibrary(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTagsByLibrary(ctx, library.ID)
	if err != nil {
		return nil, s.internalError(ctx, "list library tags", err)
	}
	output := &tagsOutput{Body: make([]Tag, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, tagFromRow(row, library.PublicID))
	}
	return output, nil
}

func (s *Service) createTag(ctx context.Context, input *createTagInput) (*tagOutput, error) {
	library, err := s.ownedLibrary(ctx, input.LibraryID)
	if err != nil {
		return nil, err
	}
	name, encryptedName, nameToken, color, encryptedColor, err := validateTag(library.EncryptionMode, input.Body)
	if err != nil {
		return nil, err
	}
	var row db.Tag
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		row, err = queries.CreateTag(ctx, db.CreateTagParams{
			PublicID: id.New(id.Tag), LibraryID: library.ID, Name: name,
			EncryptedName: encryptedName, NameToken: nameToken, Color: color, EncryptedColor: encryptedColor,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, queries, tagEvent(ctx, audit.TagCreated, library, row))
	})
	if isConstraint(err, "tags_plain_name_key") || isConstraint(err, "tags_encrypted_name_key") {
		return nil, huma.Error409Conflict("A tag with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create tag", err)
	}
	return &tagOutput{Body: tagFromRow(row, library.PublicID)}, nil
}

func (s *Service) updateTag(ctx context.Context, input *updateTagInput) (*tagOutput, error) {
	library, err := s.ownedLibrary(ctx, input.LibraryID)
	if err != nil {
		return nil, err
	}
	tag, err := s.ownedTag(ctx, input.TagID, library.ID)
	if err != nil {
		return nil, err
	}
	name, encryptedName, nameToken, color, encryptedColor, err := validateTag(library.EncryptionMode, input.Body)
	if err != nil {
		return nil, err
	}
	var row db.Tag
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		row, err = queries.UpdateTag(ctx, db.UpdateTagParams{
			ID: tag.ID, Name: name, EncryptedName: encryptedName, NameToken: nameToken, Color: color, EncryptedColor: encryptedColor,
		})
		if err != nil {
			return err
		}
		event := tagEvent(ctx, audit.TagUpdated, library, row)
		if tag.Name != row.Name {
			event.Details.PreviousName = tag.Name.String
		}
		return audit.Record(ctx, queries, event)
	})
	if isConstraint(err, "tags_plain_name_key") || isConstraint(err, "tags_encrypted_name_key") {
		return nil, huma.Error409Conflict("A tag with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "update tag", err)
	}
	return &tagOutput{Body: tagFromRow(row, library.PublicID)}, nil
}

func (s *Service) deleteTag(ctx context.Context, input *tagInput) (*struct{}, error) {
	library, err := s.ownedLibrary(ctx, input.LibraryID)
	if err != nil {
		return nil, err
	}
	tag, err := s.ownedTag(ctx, input.TagID, library.ID)
	if err != nil {
		return nil, err
	}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		if _, err := queries.DeleteTag(ctx, tag.ID); err != nil {
			return err
		}
		return audit.Record(ctx, queries, tagEvent(ctx, audit.TagDeleted, library, tag))
	})
	if err != nil {
		return nil, s.internalError(ctx, "delete tag", err)
	}
	return nil, nil
}

func tagEvent(ctx context.Context, action audit.Action, library db.GetLibraryByPublicIDAndOwnerRow, tag db.Tag) audit.Event {
	user, _ := auth.UserFromContext(ctx)
	return audit.Event{
		Action: action, ActorID: user.ID, TargetID: tag.PublicID,
		Details: audit.Details{
			Name: tag.Name.String, Encrypted: library.EncryptionMode == EncryptionE2EE,
			LibraryID: library.PublicID, LibraryName: library.Name.String,
		},
	}
}

func (s *Service) ownedLibrary(ctx context.Context, publicID string) (db.GetLibraryByPublicIDAndOwnerRow, error) {
	user, _ := auth.UserFromContext(ctx)
	library, err := s.queries.GetLibraryByPublicIDAndOwner(ctx, db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: publicID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library, libraryNotFound()
	}
	if err != nil {
		return library, s.internalError(ctx, "get library for tag", err)
	}
	return library, nil
}

func (s *Service) ownedTag(ctx context.Context, publicID string, libraryID int64) (db.Tag, error) {
	user, _ := auth.UserFromContext(ctx)
	tag, err := s.queries.GetTagByPublicIDAndOwner(ctx, db.GetTagByPublicIDAndOwnerParams{
		PublicID: publicID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && tag.LibraryID != libraryID {
		return tag, huma.Error404NotFound("The tag does not exist.")
	}
	if err != nil {
		return tag, s.internalError(ctx, "get tag", err)
	}
	return tag, nil
}

func validateTag(mode string, body tagBody) (pgtype.Text, []byte, []byte, pgtype.Text, []byte, error) {
	if mode == EncryptionE2EE {
		if body.Color != "" || len(body.EncryptedColor) == 0 {
			return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Send an encrypted tag color.")
		}
		name, encryptedName, nameToken, err := ValidateNodeName(mode, body.Name, body.EncryptedName, body.NameToken)
		if err != nil {
			return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Enter a valid tag name.")
		}
		return name, encryptedName, nameToken, pgtype.Text{}, body.EncryptedColor, nil
	}
	if len(body.EncryptedColor) != 0 {
		return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Send a plain tag color.")
	}
	color := strings.ToUpper(body.Color)
	if !tagColorPattern.MatchString(color) {
		return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Use a six-digit hexadecimal tag color.")
	}
	name, encryptedName, nameToken, err := ValidateNodeName(mode, body.Name, body.EncryptedName, body.NameToken)
	if err != nil {
		return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Enter a valid tag name.")
	}
	if name.Valid && utf8.RuneCountInString(name.String) > 100 {
		return pgtype.Text{}, nil, nil, pgtype.Text{}, nil, huma.Error422UnprocessableEntity("Use a tag name with 100 characters or fewer.")
	}
	return name, encryptedName, nameToken, pgtype.Text{String: color, Valid: true}, nil, nil
}

func tagFromRow(row db.Tag, libraryID string) Tag {
	tag := Tag{
		ID: row.PublicID, LibraryID: libraryID, EncryptedName: row.EncryptedName,
		NameToken: row.NameToken, EncryptedColor: row.EncryptedColor,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.Name.Valid {
		tag.Name = row.Name.String
	}
	if row.Color.Valid {
		tag.Color = row.Color.String
	}
	return tag
}

func (s *Service) tagsForNodes(ctx context.Context, nodes []db.Node, libraryPublicID string) (map[int64][]Tag, error) {
	result := make(map[int64][]Tag, len(nodes))
	ids := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		result[node.ID] = []Tag{}
		ids = append(ids, node.ID)
	}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.queries.ListTagsForNodes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.NodeID] = append(result[row.NodeID], tagFromRow(db.Tag{
			ID: row.ID, PublicID: row.PublicID, LibraryID: row.LibraryID, Name: row.Name,
			EncryptedName: row.EncryptedName, NameToken: row.NameToken, Color: row.Color, EncryptedColor: row.EncryptedColor,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}, libraryPublicID))
	}
	return result, nil
}
