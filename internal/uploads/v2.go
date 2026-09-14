package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryption"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const FormatE2EEV2 = "stocat-framed-v2"

type createV2Body struct {
	LibraryID        string                   `json:"library_id" maxLength:"64"`
	TargetNodeID     string                   `json:"target_node_id,omitempty" maxLength:"64" doc:"Send for a replacement."`
	ExpectedRevision string                   `json:"expected_revision,omitempty" pattern:"^[1-9][0-9]{0,17}$" doc:"Send the content revision of the target."`
	Size             int64                    `json:"size" minimum:"80" doc:"Size of the complete stocat-framed-v2 ciphertext."`
	Metadata         *encryption.SignedRecord `json:"metadata,omitempty" doc:"Send for a new file."`
	ParentEnvelope   *encryption.SignedRecord `json:"parent_envelope,omitempty" doc:"Send for a new file."`
	NameToken        string                   `json:"name_token,omitempty" maxLength:"43" pattern:"^[A-Za-z0-9_-]*$" doc:"Send for a new file."`
}

type createV2Input struct {
	Body createV2Body
}

type completeV2Input struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		FileKey        string                  `json:"file_key" minLength:"1" maxLength:"21846" pattern:"^[A-Za-z0-9_-]+$"`
		Manifest       encryption.SignedRecord `json:"manifest"`
		CurrentVersion encryption.SignedRecord `json:"current_version"`
	}
}

func (s *Service) RegisterV2(api huma.API) {
	group := huma.NewGroup(api, "/uploads")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Encrypted uploads"} })
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-uploads-create", Method: http.MethodPost, Path: "",
		Summary: "Create an encrypted upload session", DefaultStatus: http.StatusCreated, MaxBodyBytes: 256 << 10,
		Description: "Send the ciphertext to upload_url with the resumable upload protocol.",
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusRequestEntityTooLarge,
			http.StatusUnprocessableEntity, http.StatusInsufficientStorage},
	}, s.createV2)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-uploads-complete", Method: http.MethodPost, Path: "/{id}/complete",
		Summary: "Complete an encrypted upload transfer", MaxBodyBytes: 128 << 10,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.completeV2)
}

type v2Owner struct {
	binding    encryption.Binding
	signingKey []byte
}

func (s *Service) v2Owner(ctx context.Context, user auth.User) (v2Owner, error) {
	binding, err := encryption.LoadBinding(ctx, s.queries, user.PublicID)
	if err != nil {
		return v2Owner{}, s.databaseError(ctx, "load encryption binding", err)
	}
	key, _, err := encryption.SigningKey(ctx, s.queries, user.ID)
	if errors.Is(err, encryption.ErrNotConfigured) {
		return v2Owner{}, huma.Error409Conflict("Set up account encryption first.")
	}
	if err != nil {
		return v2Owner{}, s.databaseError(ctx, "load signing key", err)
	}
	return v2Owner{binding: binding, signingKey: key}, nil
}

func (s *Service) createV2(ctx context.Context, input *createV2Input) (*uploadOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	body := input.Body
	if body.Size > s.maxSize {
		return nil, huma.Error413RequestEntityTooLarge("The file exceeds the upload limit.")
	}
	replacement := body.TargetNodeID != ""
	newFile := body.Metadata != nil && body.ParentEnvelope != nil && body.NameToken != "" && body.ExpectedRevision == ""
	if replacement == newFile || replacement && (body.ExpectedRevision == "" || body.Metadata != nil || body.ParentEnvelope != nil || body.NameToken != "") {
		return nil, huma.Error422UnprocessableEntity("Send node records for a new file, or a target and its revision for a replacement.")
	}
	owner, err := s.v2Owner(ctx, user)
	if err != nil {
		return nil, err
	}
	var session db.UploadSession
	var backendName string
	err = db.InTx(ctx, s.pool, func(q *db.Queries) error {
		if err := q.LockUploadAdmission(ctx); err != nil {
			return err
		}
		library, err := q.LockV2LibraryForUpload(ctx, db.LockV2LibraryForUploadParams{PublicID: body.LibraryID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		backendName = library.BackendName
		publicID := id.New(id.Upload)
		params := db.CreateV2UploadSessionParams{
			PublicID: publicID, OwnerID: user.ID, LibraryID: library.ID, DeclaredSize: body.Size,
			StagingKey: publicID + ".part", DestinationKey: "objects/" + library.PublicID + "/" + publicID,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(s.lifetime), Valid: true},
		}
		if replacement {
			target, loadErr := q.GetV2NodeByPublicIDAndOwner(ctx, db.GetV2NodeByPublicIDAndOwnerParams{PublicID: body.TargetNodeID, OwnerID: user.ID})
			if loadErr != nil || target.LibraryID != library.ID || target.Kind != "file" || target.TrashedAt.Valid {
				return errInvalidTarget
			}
			revision, _ := strconv.ParseInt(body.ExpectedRevision, 10, 64)
			params.ParentID, params.TargetNodeID, params.ExpectedRevision = target.ParentID.Int64, validInt(target.ID), validInt(revision)
		} else if err = s.newFileRecords(ctx, q, owner, library, body, &params); err != nil {
			return err
		}
		if err = s.admit(ctx, q, user.ID, library.BackendID, body.Size); err != nil {
			return err
		}
		session, err = q.CreateV2UploadSession(ctx, params)
		return err
	})
	if err != nil {
		return nil, s.createError(ctx, err, backendName)
	}
	if err := s.createStagingFile(ctx, session); err != nil {
		return nil, err
	}
	return &uploadOutput{Body: uploadFromRow(session)}, nil
}

func (s *Service) newFileRecords(ctx context.Context, q *db.Queries, owner v2Owner, library db.LockV2LibraryForUploadRow, body createV2Body, params *db.CreateV2UploadSessionParams) error {
	metadata, metadataData, metadataSignature, err := owner.binding.ParseSigned("metadata", "node-metadata", *body.Metadata, owner.signingKey)
	if err != nil {
		return err
	}
	if err = encryption.Expect("metadata", metadata, "library_id", library.PublicID, "node_epoch", "1", "revision", "1"); err != nil {
		return err
	}
	envelope, envelopeData, envelopeSignature, err := owner.binding.ParseSigned("parent_envelope", "parent-envelope", *body.ParentEnvelope, owner.signingKey)
	if err != nil {
		return err
	}
	parent, err := q.GetV2NodeByPublicIDAndOwner(ctx, db.GetV2NodeByPublicIDAndOwnerParams{
		PublicID: envelope.Fields["parent_id"], OwnerID: params.OwnerID,
	})
	if err != nil || parent.LibraryID != library.ID || parent.Kind != "folder" || parent.TrashedAt.Valid {
		return pgx.ErrNoRows
	}
	if err = encryption.Expect("parent_envelope", envelope, "library_id", library.PublicID, "child_id", metadata.Fields["node_id"],
		"parent_epoch", strconv.FormatInt(parent.KeyEpoch, 10), "child_epoch", "1", "generation", "1", "revision", "1"); err != nil {
		return err
	}
	token, err := encryptionv2.DecodeBase64URL(body.NameToken, 32)
	if err != nil {
		return huma.Error422UnprocessableEntity("Send a 32-byte name token in unpadded base64url.")
	}
	params.ParentID, params.NameToken = parent.ID, token
	params.NodePublicID = pgtype.Text{String: metadata.Fields["node_id"], Valid: true}
	params.EncryptedMetadata, params.MetadataSignature = metadataData, metadataSignature
	params.ParentEnvelope, params.ParentEnvelopeSignature = envelopeData, envelopeSignature
	return nil
}

func (s *Service) completeV2(ctx context.Context, input *completeV2Input) (*uploadOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetUploadSessionByPublicIDAndOwner(ctx, db.GetUploadSessionByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.EncryptionFormat.String != FormatE2EEV2 {
		return nil, huma.Error404NotFound("The upload session does not exist.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "get encrypted upload for completion", err)
	}
	if row.State != stateUploaded {
		if row.State == stateFinalizing || row.State == stateCompleted {
			return &uploadOutput{Body: uploadFromRow(row)}, nil
		}
		return nil, huma.Error409Conflict("Upload all bytes before completion.")
	}
	owner, err := s.v2Owner(ctx, user)
	if err != nil {
		return nil, err
	}
	publication, err := s.queries.GetUploadPublication(ctx, row.ID)
	if err != nil {
		return nil, s.databaseError(ctx, "get encrypted upload publication", err)
	}
	nodeID, epoch, revision := row.NodePublicID.String, int64(1), int64(2)
	if !row.NodePublicID.Valid {
		if !row.TargetNodeID.Valid {
			return nil, huma.Error409Conflict("The destination changed while the upload was active.")
		}
		target, loadErr := s.queries.GetNodeByID(ctx, row.TargetNodeID.Int64)
		if loadErr != nil {
			return nil, s.databaseError(ctx, "get encrypted upload target", loadErr)
		}
		nodeID, epoch, revision = target.PublicID, target.KeyEpoch, row.ExpectedRevision.Int64+1
	}
	records, err := owner.completionRecords(input, publication.LibraryPublicID, nodeID, epoch, revision, row.DeclaredSize)
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if s.queue == nil {
			return fmt.Errorf("publication queue is unavailable")
		}
		records.ID = row.ID
		if _, err := db.New(tx).SetV2UploadFinalizing(ctx, records); err != nil {
			return err
		}
		_, err := s.queue.InsertTx(ctx, tx, FinalizeArgs{UploadID: row.ID}, nil)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error409Conflict("The upload state changed.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "complete encrypted upload", err)
	}
	row.State = stateFinalizing
	return &uploadOutput{Body: uploadFromRow(row)}, nil
}

func (o v2Owner) completionRecords(input *completeV2Input, libraryID, nodeID string, epoch, revision, declaredSize int64) (db.SetV2UploadFinalizingParams, error) {
	var params db.SetV2UploadFinalizingParams
	body := input.Body
	nodeEpoch, nextRevision := strconv.FormatInt(epoch, 10), strconv.FormatInt(revision, 10)
	fileKey, fileKeyData, err := o.binding.ParseRecord("file_key", "file-key", body.FileKey)
	if err != nil {
		return params, err
	}
	if err = encryption.Expect("file_key", fileKey, "library_id", libraryID, "node_id", nodeID, "node_epoch", nodeEpoch, "generation", "1"); err != nil {
		return params, err
	}
	manifest, manifestData, manifestSignature, err := o.binding.ParseSigned("manifest", "version-manifest", body.Manifest, o.signingKey)
	if err != nil {
		return params, err
	}
	keyHash := sha256.Sum256(fileKeyData)
	if err = encryption.Expect("manifest", manifest, "library_id", libraryID, "node_id", nodeID,
		"version_id", fileKey.Fields["version_id"], "content_id", fileKey.Fields["content_id"], "node_epoch", nodeEpoch,
		"revision", nextRevision, "key_envelope_hash", base64.RawURLEncoding.EncodeToString(keyHash[:])); err != nil {
		return params, err
	}
	headerBytes, _ := encryptionv2.DecodeBase64URL(manifest.Fields["header"], encryptionv2.HeaderSize)
	header, err := encryptionv2.ParseFileHeader(headerBytes)
	if err != nil {
		return params, encryption.Invalid("manifest", err)
	}
	if size, sizeErr := header.CiphertextSize(); sizeErr != nil || int64(size) != declaredSize {
		return params, encryption.Invalid("manifest", errors.New("header ciphertext size differs from the upload size"))
	}
	current, currentData, currentSignature, err := o.binding.ParseSigned("current_version", "current-version", body.CurrentVersion, o.signingKey)
	if err != nil {
		return params, err
	}
	if err = encryption.Expect("current_version", current, "library_id", libraryID, "node_id", nodeID, "node_epoch", nodeEpoch,
		"revision", nextRevision, "version_id", manifest.Fields["version_id"]); err != nil {
		return params, err
	}
	return db.SetV2UploadFinalizingParams{
		FileKeyRecord: fileKeyData, ManifestRecord: manifestData, ManifestSignature: manifestSignature,
		CurrentVersionRecord: currentData, CurrentVersionSignature: currentSignature,
	}, nil
}

// finalizeV2 stores the ciphertext only when it matches the header and hash in the signed manifest.
func (s *Service) finalizeV2(ctx context.Context, stores *storage.Service, upload db.GetUploadPublicationRow) error {
	checksum, err := s.hashStaging(upload.StagingKey)
	if err != nil {
		return err
	}
	manifest, err := encryptionv2.ParseRecord(upload.ManifestRecord)
	if err != nil {
		return s.finishFailedUpload(ctx, upload.ID, "invalid_encryption", "The signed manifest is invalid.")
	}
	header, _ := encryptionv2.DecodeBase64URL(manifest.Fields["header"], encryptionv2.HeaderSize)
	staged, err := s.readStagingHeader(upload.StagingKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(staged, header) || manifest.Fields["ciphertext_hash"] != base64.RawURLEncoding.EncodeToString(checksum[:]) {
		return s.finishFailedUpload(ctx, upload.ID, "invalid_encryption", "The encrypted file does not match its signed manifest.")
	}
	parsed, err := encryptionv2.ParseFileHeader(header)
	if err != nil {
		return s.finishFailedUpload(ctx, upload.ID, "invalid_encryption", "The encrypted file header is invalid.")
	}
	store, err := stores.ObjectStore(ctx, upload.BackendPublicID)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := s.putStaging(ctx, store, upload, checksum); err != nil {
		return err
	}
	err = s.publishV2(ctx, upload, manifest, checksum, int64(parsed.PlaintextSize))
	if errors.Is(err, errPublishConflict) {
		_ = store.Delete(ctx, upload.DestinationKey)
		return s.finishUpload(ctx, upload.ID, "conflict", "conflict", "The destination changed while the upload was active.")
	}
	if err != nil {
		return err
	}
	s.removeStaging(ctx, upload)
	return nil
}

func (s *Service) readStagingHeader(key string) ([]byte, error) {
	file, err := s.staging.Open(key)
	if err != nil {
		return nil, fmt.Errorf("open staged upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, encryptionv2.HeaderSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return nil, fmt.Errorf("read staged encryption header: %w", err)
	}
	return header, nil
}

func (s *Service) publishV2(ctx context.Context, upload db.GetUploadPublicationRow, manifest encryptionv2.Record, checksum [sha256.Size]byte, plaintextSize int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		current, err := q.GetUploadSessionForUpdate(ctx, upload.ID)
		if err != nil || current.State != stateFinalizing || current.ExpectedRevision.Valid && !current.TargetNodeID.Valid {
			if err == nil {
				err = errPublishConflict
			}
			return err
		}
		if err = q.LockLibraryByID(ctx, upload.LibraryID); err != nil {
			return err
		}
		blob, err := q.CreateBlob(ctx, db.CreateBlobParams{
			PublicID: id.New(id.Blob), LibraryID: upload.LibraryID, SizeBytes: upload.DeclaredSize,
			CiphertextSha256: checksum[:], EncryptionFormat: textValue(FormatE2EEV2),
		})
		if err != nil {
			return err
		}
		location, err := q.CreateBlobLocation(ctx, db.CreateBlobLocationParams{
			PublicID: id.New(id.BlobLocation), BlobID: blob.ID, LibraryID: upload.LibraryID,
			BackendID: upload.BackendID, ObjectKey: upload.DestinationKey,
		})
		if err != nil {
			return err
		}
		if _, err = q.MarkBlobLocationAvailable(ctx, location.ID); err != nil {
			return err
		}
		node, err := s.publicationNode(ctx, q, upload, manifest)
		if err != nil {
			return err
		}
		ordinal, err := q.NextFileVersionOrdinal(ctx, node.ID)
		if err != nil {
			return err
		}
		version, err := q.CreateFileVersion(ctx, db.CreateFileVersionParams{
			PublicID: manifest.Fields["version_id"], NodeID: node.ID, LibraryID: upload.LibraryID,
			Ordinal: int64(ordinal), BlobID: blob.ID, SizeBytes: plaintextSize,
		})
		if err != nil {
			return classifyPublishError(err)
		}
		contentID, _ := encryptionv2.DecodeBase64URL(manifest.Fields["content_id"], 16)
		if err = q.CreateFileVersionKey(ctx, db.CreateFileVersionKeyParams{
			VersionID: version.ID, NodeID: node.ID, NodeEpoch: node.KeyEpoch, Generation: 1, ContentID: contentID,
			EncryptedContentKey: upload.FileKeyRecord, SignedManifest: upload.ManifestRecord,
			ManifestSignature: upload.ManifestSignature,
		}); err != nil {
			return err
		}
		revision, err := q.SetV2CurrentVersion(ctx, db.SetV2CurrentVersionParams{
			CurrentVersionID: validInt(version.ID), CurrentVersionPointer: upload.CurrentVersionRecord,
			CurrentVersionSignature: upload.CurrentVersionSignature, ID: node.ID,
		})
		if err != nil {
			return err
		}
		if strconv.FormatInt(revision, 10) != manifest.Fields["revision"] {
			return errPublishConflict
		}
		if err = q.CompleteUploadSession(ctx, db.CompleteUploadSessionParams{
			ID: upload.ID, PublishedNodeID: validInt(node.ID), PublishedVersionID: validInt(version.ID),
		}); err != nil {
			return err
		}
		action := audit.FileUploaded
		if upload.TargetNodeID.Valid {
			action = audit.FileReplaced
		}
		event, err := audit.FileEvent(ctx, q, action, node.ID)
		if err != nil {
			return err
		}
		event.ActorID = upload.OwnerID
		event.Details.SizeBytes = plaintextSize
		return audit.Record(ctx, q, event)
	})
}

// publicationNode locks the replacement target or creates the new file node with its parent envelope.
func (s *Service) publicationNode(ctx context.Context, q *db.Queries, upload db.GetUploadPublicationRow, manifest encryptionv2.Record) (db.Node, error) {
	if upload.TargetNodeID.Valid {
		node, err := q.LockNodeForPublication(ctx, upload.TargetNodeID.Int64)
		if err != nil || node.LibraryID != upload.LibraryID || node.Kind != "file" || node.TrashedAt.Valid ||
			node.Revision != upload.ExpectedRevision.Int64 || strconv.FormatInt(node.KeyEpoch, 10) != manifest.Fields["node_epoch"] {
			return node, errPublishConflict
		}
		return node, nil
	}
	envelope, err := encryptionv2.ParseRecord(upload.ParentEnvelope)
	if err != nil {
		return db.Node{}, fmt.Errorf("parse stored parent envelope: %w", err)
	}
	parentEpoch, _ := strconv.ParseInt(envelope.Fields["parent_epoch"], 10, 64)
	node, err := q.CreateV2UploadFileNode(ctx, db.CreateV2UploadFileNodeParams{
		PublicID: upload.NodePublicID.String, LibraryID: upload.LibraryID, ParentID: validInt(upload.ParentID),
		NameToken: upload.NameToken, EncryptedMetadata: upload.EncryptedMetadata, MetadataSignature: upload.MetadataSignature,
	})
	if err != nil {
		return node, classifyPublishError(err)
	}
	return node, q.CreateNodeKeyEnvelope(ctx, db.CreateNodeKeyEnvelopeParams{
		ChildNodeID: node.ID, ParentNodeID: upload.ParentID, LibraryID: upload.LibraryID, ChildEpoch: 1,
		ParentEpoch: parentEpoch, Generation: 1, Ciphertext: upload.ParentEnvelope,
		OwnerSignature: upload.ParentEnvelopeSignature,
	})
}
