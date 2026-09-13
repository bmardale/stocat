package files

import (
	"context"
	"errors"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const uniqueViolation = "23505"

type renameInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Name          string `json:"name,omitempty" maxLength:"255"`
		EncryptedName []byte `json:"encrypted_name,omitempty"`
		NameToken     []byte `json:"name_token,omitempty"`
	}
}

type nodeOutput struct{ Body libraries.Node }

func (s *Service) rename(ctx context.Context, input *renameInput) (*nodeOutput, error) {
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	name, encryptedName, nameToken, err := libraries.ValidateNodeName(file.EncryptionMode, input.Body.Name, input.Body.EncryptedName, input.Body.NameToken)
	if err != nil {
		return nil, err
	}
	row, err := s.queries.RenameFileNode(ctx, db.RenameFileNodeParams{
		ID: file.ID, Name: name, EncryptedName: encryptedName, NameToken: nameToken,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fileNotFound()
	}
	if isPgError(err, uniqueViolation) {
		return nil, huma.Error409Conflict("A file or folder with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "rename file", err)
	}
	return &nodeOutput{Body: libraries.NodeFromRow(row, file.LibraryPublicID, file.ParentPublicID)}, nil
}

func (s *Service) loadNode(ctx context.Context, publicID string) (db.GetFileNodeByPublicIDAndOwnerRow, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetFileNodeByPublicIDAndOwner(ctx, db.GetFileNodeByPublicIDAndOwnerParams{
		NodePublicID: publicID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, fileNotFound()
	}
	if err != nil {
		return row, s.internalError(ctx, "load file node", err)
	}
	return row, nil
}

func isPgError(err error, code string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == code
}
