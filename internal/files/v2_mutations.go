package files

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryption"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type EncryptedTrashedFile struct {
	ID               string                  `json:"id"`
	LibraryID        string                  `json:"library_id"`
	ParentID         string                  `json:"parent_id"`
	KeyEpoch         string                  `json:"key_epoch"`
	MetadataRevision string                  `json:"metadata_revision"`
	Revision         string                  `json:"revision"`
	NameToken        string                  `json:"name_token"`
	Metadata         encryption.SignedRecord `json:"metadata"`
	ParentEnvelope   encryption.SignedRecord `json:"parent_envelope"`
	TrashedAt        time.Time               `json:"trashed_at"`
	DeleteAfter      time.Time               `json:"delete_after"`
}

type encryptedTrashPage struct {
	Items      []EncryptedTrashedFile `json:"items" nullable:"false"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type encryptedTrashOutput struct{ Body encryptedTrashPage }

type encryptedTrashInput struct {
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
}

type encryptedMoveInput struct {
	ID      string `path:"id" maxLength:"64"`
	IfMatch string `header:"If-Match" required:"true" doc:"Send the revision of the current parent envelope in quotes."`
	Body    struct {
		ParentEnvelope encryption.SignedRecord `json:"parent_envelope"`
		NameToken      string                  `json:"name_token" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]+$"`
	}
}

func (s *Service) registerV2Mutations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "encrypted-files-trash-list", Method: http.MethodGet, Path: "/trash",
		Summary: "List trashed encrypted files", Errors: []int{http.StatusUnprocessableEntity},
	}, s.listTrashV2)
	huma.Register(api, huma.Operation{
		OperationID: "encrypted-files-move", Method: http.MethodPost, Path: "/{id}/move",
		Summary:       "Move an encrypted file within its library",
		Description:   "The parent envelope names the destination folder and uses the next envelope revision.",
		DefaultStatus: http.StatusNoContent, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.moveV2)
	huma.Register(api, huma.Operation{
		OperationID: "encrypted-files-delete", Method: http.MethodDelete, Path: "/{id}",
		Summary: "Move an encrypted file to trash", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.trashV2)
	huma.Register(api, huma.Operation{
		OperationID: "encrypted-files-restore", Method: http.MethodPost, Path: "/{id}/restore",
		Summary: "Restore a trashed encrypted file", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusConflict, http.StatusNotFound},
	}, s.restoreV2)
	huma.Register(api, huma.Operation{
		OperationID: "encrypted-files-delete-permanently", Method: http.MethodDelete, Path: "/{id}/permanent",
		Summary: "Permanently delete a trashed encrypted file", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusServiceUnavailable},
	}, s.deleteV2)
}

func (s *Service) listTrashV2(ctx context.Context, input *encryptedTrashInput) (*encryptedTrashOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	afterID := int64(0)
	if input.Cursor != "" {
		cursor, err := s.queries.GetV2FileNodeByPublicIDAndOwner(ctx, db.GetV2FileNodeByPublicIDAndOwnerParams{
			NodePublicID: input.Cursor, OwnerID: user.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !cursor.TrashedAt.Valid {
			return nil, huma.Error422UnprocessableEntity("The trash cursor is not valid.")
		}
		if err != nil {
			return nil, s.internalError(ctx, "load encrypted trash cursor", err)
		}
		afterID = cursor.ID
	}
	limit := input.Limit
	if limit == 0 {
		limit = trashPageSize
	}
	rows, err := s.queries.ListV2TrashedFilesByOwner(ctx, db.ListV2TrashedFilesByOwnerParams{
		OwnerID: user.ID, AfterID: afterID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, s.internalError(ctx, "list encrypted trash", err)
	}
	output := &encryptedTrashOutput{Body: encryptedTrashPage{Items: make([]EncryptedTrashedFile, 0, min(len(rows), int(limit)))}}
	if len(rows) > int(limit) {
		output.Body.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		output.Body.Items = append(output.Body.Items, EncryptedTrashedFile{
			ID: row.PublicID, LibraryID: row.LibraryPublicID, ParentID: row.ParentPublicID,
			KeyEpoch: strconv.FormatInt(row.KeyEpoch, 10), MetadataRevision: strconv.FormatInt(row.MetadataRevision, 10),
			Revision:       strconv.FormatInt(row.Revision, 10),
			NameToken:      base64.RawURLEncoding.EncodeToString(row.NameToken),
			Metadata:       encryption.Encode(row.EncryptedMetadata, row.MetadataSignature),
			ParentEnvelope: encryption.Encode(row.ParentEnvelope, row.ParentEnvelopeSignature),
			TrashedAt:      row.TrashedAt.Time, DeleteAfter: row.TrashedAt.Time.Add(trashLifetime),
		})
	}
	return output, nil
}

// moveV2 changes the parent and the parent envelope in one transaction.
// The file never has a parent without a matching envelope.
func (s *Service) moveV2(ctx context.Context, input *encryptedMoveInput) (*struct{}, error) {
	user, _ := auth.UserFromContext(ctx)
	binding, err := encryption.LoadBinding(ctx, s.queries, user.PublicID)
	if err != nil {
		return nil, s.internalError(ctx, "load encryption binding", err)
	}
	signingKey, _, err := encryption.SigningKey(ctx, s.queries, user.ID)
	if errors.Is(err, encryption.ErrNotConfigured) {
		return nil, huma.Error409Conflict("Set up account encryption first.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "load signing key", err)
	}
	envelope, envelopeData, envelopeSignature, err := binding.ParseSigned("parent_envelope", "parent-envelope", input.Body.ParentEnvelope, signingKey)
	if err != nil {
		return nil, err
	}
	token, err := encryptionv2.DecodeBase64URL(input.Body.NameToken, 32)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("Send a 32-byte name token in unpadded base64url.")
	}
	err = db.InTx(ctx, s.pool, func(q *db.Queries) error {
		file, err := q.GetV2FileNodeByPublicIDAndOwner(ctx, db.GetV2FileNodeByPublicIDAndOwnerParams{NodePublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		if file.TrashedAt.Valid {
			return pgx.ErrNoRows
		}
		current, err := q.LockV2NodeEnvelope(ctx, db.LockV2NodeEnvelopeParams{
			ChildNodeID: file.ID, ChildEpoch: file.KeyEpoch, Generation: file.VisibleEncryptionGeneration,
		})
		if err != nil {
			return err
		}
		previous, err := encryptionv2.ParseRecord(current.Ciphertext)
		if err != nil {
			return err
		}
		if input.IfMatch != `"`+previous.Fields["revision"]+`"` {
			return huma.Error409Conflict("The file changed. Load it again.")
		}
		parent, err := q.GetV2NodeByPublicIDAndOwner(ctx, db.GetV2NodeByPublicIDAndOwnerParams{
			PublicID: envelope.Fields["parent_id"], OwnerID: user.ID,
		})
		if err != nil || parent.LibraryID != file.LibraryID || parent.Kind != "folder" || parent.TrashedAt.Valid {
			return huma.Error404NotFound("The destination folder does not exist.")
		}
		if err = encryption.Expect("parent_envelope", envelope, "library_id", file.LibraryPublicID, "child_id", file.PublicID,
			"parent_epoch", strconv.FormatInt(parent.KeyEpoch, 10), "child_epoch", strconv.FormatInt(file.KeyEpoch, 10),
			"generation", strconv.FormatInt(file.VisibleEncryptionGeneration, 10),
			"revision", strconv.FormatUint(encryption.Counter(previous, "revision")+1, 10)); err != nil {
			return err
		}
		moved, err := q.MoveV2FileNode(ctx, db.MoveV2FileNodeParams{
			ParentID: pgtype.Int8{Int64: parent.ID, Valid: true}, NameToken: token, ID: file.ID,
		})
		if err != nil {
			return err
		}
		if moved == 0 {
			return pgx.ErrNoRows
		}
		if err = q.UpdateNodeKeyEnvelopeParent(ctx, db.UpdateNodeKeyEnvelopeParentParams{
			ParentNodeID: parent.ID, ParentEpoch: parent.KeyEpoch, Ciphertext: envelopeData, OwnerSignature: envelopeSignature,
			ChildNodeID: file.ID, ChildEpoch: file.KeyEpoch, Generation: file.VisibleEncryptionGeneration,
		}); err != nil {
			return err
		}
		return recordFileEvent(ctx, q, audit.FileMoved, file.ID, user.ID, nil)
	})
	if err != nil {
		return nil, s.v2Error(ctx, "move encrypted file", err)
	}
	return nil, nil
}

func (s *Service) trashV2(ctx context.Context, input *encryptedFileInput) (*struct{}, error) {
	user, _ := auth.UserFromContext(ctx)
	err := db.InTx(ctx, s.pool, func(q *db.Queries) error {
		file, err := q.GetV2FileNodeByPublicIDAndOwner(ctx, db.GetV2FileNodeByPublicIDAndOwnerParams{NodePublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		if _, err = q.TrashFileNode(ctx, file.ID); err != nil {
			return err
		}
		return recordFileEvent(ctx, q, audit.FileTrashed, file.ID, user.ID, nil)
	})
	if err != nil {
		return nil, s.v2Error(ctx, "trash encrypted file", err)
	}
	return nil, nil
}

func (s *Service) restoreV2(ctx context.Context, input *encryptedFileInput) (*struct{}, error) {
	user, _ := auth.UserFromContext(ctx)
	err := db.InTx(ctx, s.pool, func(q *db.Queries) error {
		file, err := q.GetV2FileNodeByPublicIDAndOwner(ctx, db.GetV2FileNodeByPublicIDAndOwnerParams{NodePublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		if _, err = q.RestoreFileNode(ctx, file.ID); err != nil {
			return err
		}
		return recordFileEvent(ctx, q, audit.FileRestored, file.ID, user.ID, nil)
	})
	if err != nil {
		return nil, s.v2Error(ctx, "restore encrypted file", err)
	}
	return nil, nil
}

func (s *Service) deleteV2(ctx context.Context, input *encryptedFileInput) (*struct{}, error) {
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("File deletion is temporarily unavailable.")
	}
	user, _ := auth.UserFromContext(ctx)
	file, err := s.queries.GetV2FileNodeByPublicIDAndOwner(ctx, db.GetV2FileNodeByPublicIDAndOwnerParams{NodePublicID: input.ID, OwnerID: user.ID})
	if err == nil && !file.TrashedAt.Valid {
		err = pgx.ErrNoRows
	}
	if err == nil {
		err = s.permanentlyDelete(ctx, file.ID, false)
	}
	if isUploadReference(err) {
		return nil, huma.Error409Conflict("An upload changed the file during deletion. Try again.")
	}
	if err != nil {
		return nil, s.v2Error(ctx, "delete encrypted file", err)
	}
	return nil, nil
}

// v2Error keeps client errors from inside a transaction and hides other errors.
func (s *Service) v2Error(ctx context.Context, operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fileNotFound()
	}
	if statusErr, ok := errors.AsType[huma.StatusError](err); ok {
		return statusErr
	}
	if isPgError(err, uniqueViolation) {
		return huma.Error409Conflict("A file or folder with this name already exists.")
	}
	return s.internalError(ctx, operation, err)
}
