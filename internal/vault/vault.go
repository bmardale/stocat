// Package vault serves v2 encrypted libraries, folders, and files to their owner.
package vault

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryption"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/bmardale/stocat/internal/libraries"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	kindFolder   = "folder"
	kindFile     = "file"
	pageSize     = 200
	maxBodyBytes = 256 << 10
	// The nodes and libraries tables limit a complete metadata record to 64 KiB.
	maxMetadataRecord = 64 << 10
)

type EncryptedLibrary struct {
	ID                string                   `json:"id"`
	RootNodeID        string                   `json:"root_node_id"`
	RootKeyEpoch      string                   `json:"root_key_epoch"`
	MetadataRevision  string                   `json:"metadata_revision"`
	RootMetadata      encryption.SignedRecord  `json:"root_metadata"`
	OwnerRootEnvelope string                   `json:"owner_root_envelope"`
	Backend           libraries.LibraryBackend `json:"backend"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

type EncryptedNode struct {
	ID               string                   `json:"id"`
	ParentID         string                   `json:"parent_id"`
	Kind             string                   `json:"kind" enum:"file,folder"`
	KeyEpoch         string                   `json:"key_epoch"`
	MetadataRevision string                   `json:"metadata_revision"`
	Revision         string                   `json:"revision" doc:"Content revision of the node."`
	NameToken        string                   `json:"name_token"`
	Metadata         encryption.SignedRecord  `json:"metadata"`
	ParentEnvelope   encryption.SignedRecord  `json:"parent_envelope"`
	CurrentVersion   *encryption.SignedRecord `json:"current_version,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

type libraryOutput struct{ Body EncryptedLibrary }
type librariesOutput struct {
	Body []EncryptedLibrary `nullable:"false"`
}
type nodeOutput struct{ Body EncryptedNode }
type encryptedNodesPage struct {
	Items      []EncryptedNode `json:"items" nullable:"false"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
type nodesOutput struct{ Body encryptedNodesPage }

type encryptedLibraryInput struct {
	ID string `path:"id" maxLength:"64"`
}

type createEncryptedLibraryInput struct {
	Body struct {
		BackendID         string                  `json:"backend_id" minLength:"1" maxLength:"64"`
		RootMetadata      encryption.SignedRecord `json:"root_metadata" doc:"A node-metadata record with epoch 1 and revision 1."`
		OwnerRootEnvelope string                  `json:"owner_root_envelope" minLength:"1" maxLength:"21846" pattern:"^[A-Za-z0-9_-]+$"`
	}
}

type renameEncryptedLibraryInput struct {
	ID      string `path:"id" maxLength:"64"`
	IfMatch string `header:"If-Match" required:"true" doc:"Send the current metadata revision in quotes."`
	Body    struct {
		RootMetadata encryption.SignedRecord `json:"root_metadata"`
	}
}

type createEncryptedFolderInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	Body      struct {
		Metadata       encryption.SignedRecord `json:"metadata"`
		ParentEnvelope encryption.SignedRecord `json:"parent_envelope"`
		NameToken      string                  `json:"name_token" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]+$"`
	}
}

type listEncryptedNodesInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	ParentID  string `query:"parent_id" maxLength:"64"`
	Cursor    string `query:"cursor" maxLength:"64"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"200"`
}

type renameEncryptedNodeInput struct {
	ID      string `path:"id" maxLength:"64"`
	IfMatch string `header:"If-Match" required:"true" doc:"Send the current metadata revision in quotes."`
	Body    struct {
		Metadata  encryption.SignedRecord `json:"metadata"`
		NameToken string                  `json:"name_token" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]+$"`
	}
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	log     *slog.Logger
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), log: logger}
}

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api)
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Encrypted libraries"} })
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-libraries-list", Method: http.MethodGet, Path: "/libraries",
		Summary: "List encrypted libraries",
	}, s.listLibraries)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-libraries-create", Method: http.MethodPost, Path: "/libraries",
		Summary: "Create an encrypted library", DefaultStatus: http.StatusCreated, MaxBodyBytes: maxBodyBytes,
		Description: "The client generates the library and root node identifiers inside the records.",
		Errors:      []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.createLibrary)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-libraries-get", Method: http.MethodGet, Path: "/libraries/{id}",
		Summary: "Get an encrypted library", Errors: []int{http.StatusNotFound},
	}, s.getLibrary)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-libraries-rename", Method: http.MethodPatch, Path: "/libraries/{id}",
		Summary: "Replace the metadata of an encrypted library", MaxBodyBytes: maxBodyBytes,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.renameLibrary)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-libraries-delete", Method: http.MethodDelete, Path: "/libraries/{id}",
		Summary: "Delete an empty encrypted library", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusConflict, http.StatusNotFound},
	}, s.deleteLibrary)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-folders-create", Method: http.MethodPost, Path: "/libraries/{id}/folders",
		Summary: "Create an encrypted folder", DefaultStatus: http.StatusCreated, MaxBodyBytes: maxBodyBytes,
		Description: "The client generates the folder identifier inside the records.",
		Errors:      []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.createFolder)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-nodes-list", Method: http.MethodGet, Path: "/libraries/{id}/nodes",
		Summary: "List an encrypted folder", Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.listNodes)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-nodes-rename", Method: http.MethodPatch, Path: "/nodes/{id}",
		Summary: "Replace the metadata of an encrypted file or folder", MaxBodyBytes: maxBodyBytes,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.renameNode)
}

type owner struct {
	id         int64
	binding    encryption.Binding
	signingKey []byte
	generation uint64
}

func (s *Service) owner(ctx context.Context) (owner, error) {
	user, _ := auth.UserFromContext(ctx)
	binding, err := encryption.LoadBinding(ctx, s.queries, user.PublicID)
	if err != nil {
		return owner{}, s.internalError(ctx, "load encryption binding", err)
	}
	key, generation, err := encryption.SigningKey(ctx, s.queries, user.ID)
	if errors.Is(err, encryption.ErrNotConfigured) {
		return owner{}, huma.Error409Conflict("Set up account encryption first.")
	}
	if err != nil {
		return owner{}, s.internalError(ctx, "load signing key", err)
	}
	return owner{id: user.ID, binding: binding, signingKey: key, generation: generation}, nil
}

// signed parses a record, checks its binding, and verifies the signature of the current identity.
func (o owner) signed(field, kind string, value encryption.SignedRecord) (encryptionv2.Record, []byte, []byte, error) {
	record, data, signature, err := o.binding.Parse(field, kind, value)
	if err != nil {
		return record, nil, nil, err
	}
	if kind == "node-metadata" && len(data) > maxMetadataRecord {
		return record, nil, nil, encryption.Invalid(field, errors.New("record is too large"))
	}
	return record, data, signature, encryption.Verify(field, data, o.signingKey, signature)
}

// expect compares record fields with expected values. Pairs contain a field name and its value.
func expect(field string, record encryptionv2.Record, pairs ...string) error {
	for index := 0; index+1 < len(pairs); index += 2 {
		if record.Fields[pairs[index]] != pairs[index+1] {
			return encryption.Invalid(field, fmt.Errorf("%s must be %s", pairs[index], pairs[index+1]))
		}
	}
	return nil
}

func (s *Service) listLibraries(ctx context.Context, _ *struct{}) (*librariesOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	rows, err := s.queries.ListV2LibrariesByOwner(ctx, user.ID)
	if err != nil {
		return nil, s.internalError(ctx, "list encrypted libraries", err)
	}
	output := &librariesOutput{Body: make([]EncryptedLibrary, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, EncryptedLibrary{
			ID: row.PublicID, RootNodeID: row.RootNodePublicID, RootKeyEpoch: counter(row.RootKeyEpoch),
			MetadataRevision:  counter(row.RootMetadataRevision),
			RootMetadata:      encryption.Encode(row.EncryptedRootMetadata, row.RootMetadataSignature),
			OwnerRootEnvelope: base64.RawURLEncoding.EncodeToString(row.OwnerRootEnvelope),
			Backend:           libraries.LibraryBackend{ID: row.BackendPublicID, Name: row.BackendName, Type: row.BackendType},
			CreatedAt:         row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return output, nil
}

func (s *Service) getLibrary(ctx context.Context, input *encryptedLibraryInput) (*libraryOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetV2LibraryByPublicIDAndOwner(ctx, db.GetV2LibraryByPublicIDAndOwnerParams{
		PublicID: input.ID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get encrypted library", err)
	}
	return &libraryOutput{Body: EncryptedLibrary{
		ID: row.PublicID, RootNodeID: row.RootNodePublicID, RootKeyEpoch: counter(row.RootKeyEpoch),
		MetadataRevision:  counter(row.RootMetadataRevision),
		RootMetadata:      encryption.Encode(row.EncryptedRootMetadata, row.RootMetadataSignature),
		OwnerRootEnvelope: base64.RawURLEncoding.EncodeToString(row.OwnerRootEnvelope),
		Backend:           libraries.LibraryBackend{ID: row.BackendPublicID, Name: row.BackendName, Type: row.BackendType},
		CreatedAt:         row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}}, nil
}

func (s *Service) createLibrary(ctx context.Context, input *createEncryptedLibraryInput) (*libraryOutput, error) {
	o, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	metadata, metadataData, metadataSignature, err := o.signed("root_metadata", "node-metadata", input.Body.RootMetadata)
	if err != nil {
		return nil, err
	}
	if err = expect("root_metadata", metadata, "node_epoch", "1", "revision", "1"); err != nil {
		return nil, err
	}
	libraryID, rootID := metadata.Fields["library_id"], metadata.Fields["node_id"]
	root, rootData, err := o.binding.ParseRecord("owner_root_envelope", "owner-root", input.Body.OwnerRootEnvelope)
	if err != nil {
		return nil, err
	}
	if err = expect("owner_root_envelope", root, "library_id", libraryID, "node_id", rootID,
		"generation", strconv.FormatUint(o.generation, 10), "node_epoch", "1", "revision", "1"); err != nil {
		return nil, err
	}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		backend, err := queries.GetEnabledBackendByPublicID(ctx, input.Body.BackendID)
		if err != nil {
			return err
		}
		libraryRowID, err := queries.NextLibraryID(ctx)
		if err != nil {
			return err
		}
		rootRowID, err := queries.NextNodeID(ctx)
		if err != nil {
			return err
		}
		if err = queries.CreateV2Library(ctx, db.CreateV2LibraryParams{
			ID: libraryRowID, PublicID: libraryID, OwnerID: o.id, BackendID: backend.ID, RootNodeID: rootRowID,
			EncryptedRootMetadata: metadataData, OwnerRootEnvelope: rootData,
		}); err != nil {
			return err
		}
		if err = queries.CreateV2RootNode(ctx, db.CreateV2RootNodeParams{
			ID: rootRowID, PublicID: rootID, LibraryID: libraryRowID, MetadataSignature: metadataSignature,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.LibraryCreated, ActorID: o.id, TargetID: libraryID, Details: audit.Details{Encrypted: true},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The enabled storage backend does not exist.")
	}
	if err != nil {
		return nil, s.result(ctx, "create encrypted library", err)
	}
	return s.getLibrary(ctx, &encryptedLibraryInput{ID: libraryID})
}

func (s *Service) renameLibrary(ctx context.Context, input *renameEncryptedLibraryInput) (*libraryOutput, error) {
	o, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	metadata, data, signature, err := o.signed("root_metadata", "node-metadata", input.Body.RootMetadata)
	if err != nil {
		return nil, err
	}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		library, err := queries.LockV2Library(ctx, db.LockV2LibraryParams{PublicID: input.ID, OwnerID: o.id})
		if err != nil {
			return err
		}
		if input.IfMatch != quoted(library.RootMetadataRevision) {
			return huma.Error409Conflict("The library changed. Load it again.")
		}
		revision := library.RootMetadataRevision + 1
		if err = expect("root_metadata", metadata, "library_id", input.ID, "node_id", library.RootNodePublicID,
			"node_epoch", counter(library.RootKeyEpoch), "revision", counter(revision)); err != nil {
			return err
		}
		if err = queries.UpdateV2RootMetadata(ctx, db.UpdateV2RootMetadataParams{ID: library.ID, EncryptedRootMetadata: data}); err != nil {
			return err
		}
		if err = queries.UpdateV2RootMetadataSignature(ctx, db.UpdateV2RootMetadataSignatureParams{
			ID: library.RootNodeID, MetadataRevision: revision, MetadataSignature: signature,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.LibraryRenamed, ActorID: o.id, TargetID: input.ID, Details: audit.Details{Encrypted: true},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.result(ctx, "rename encrypted library", err)
	}
	return s.getLibrary(ctx, &encryptedLibraryInput{ID: input.ID})
}

func (s *Service) deleteLibrary(ctx context.Context, input *encryptedLibraryInput) (*struct{}, error) {
	user, _ := auth.UserFromContext(ctx)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		if _, err := queries.LockV2Library(ctx, db.LockV2LibraryParams{PublicID: input.ID, OwnerID: user.ID}); err != nil {
			return err
		}
		deleted, err := queries.DeleteEmptyLibrary(ctx, db.DeleteEmptyLibraryParams{PublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return huma.Error409Conflict("Delete all files before you delete this library.")
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.LibraryDeleted, ActorID: user.ID, TargetID: input.ID, Details: audit.Details{Encrypted: true},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.result(ctx, "delete encrypted library", err)
	}
	return nil, nil
}

func (s *Service) createFolder(ctx context.Context, input *createEncryptedFolderInput) (*nodeOutput, error) {
	o, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	library, err := s.queries.GetV2LibraryByPublicIDAndOwner(ctx, db.GetV2LibraryByPublicIDAndOwnerParams{
		PublicID: input.LibraryID, OwnerID: o.id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get encrypted library for folder", err)
	}
	metadata, metadataData, metadataSignature, err := o.signed("metadata", "node-metadata", input.Body.Metadata)
	if err != nil {
		return nil, err
	}
	if err = expect("metadata", metadata, "library_id", library.PublicID, "node_epoch", "1", "revision", "1"); err != nil {
		return nil, err
	}
	envelope, envelopeData, envelopeSignature, err := o.signed("parent_envelope", "parent-envelope", input.Body.ParentEnvelope)
	if err != nil {
		return nil, err
	}
	parent, err := s.activeFolder(ctx, s.queries, envelope.Fields["parent_id"], o.id, library.ID)
	if err != nil {
		return nil, err
	}
	if err = expect("parent_envelope", envelope, "library_id", library.PublicID, "child_id", metadata.Fields["node_id"],
		"parent_epoch", counter(parent.KeyEpoch), "child_epoch", "1", "generation", "1", "revision", "1"); err != nil {
		return nil, err
	}
	token, err := decodeNameToken(input.Body.NameToken)
	if err != nil {
		return nil, err
	}
	var nodeID int64
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		nodeID, err = queries.CreateV2ChildNode(ctx, db.CreateV2ChildNodeParams{
			PublicID: metadata.Fields["node_id"], LibraryID: library.ID, ParentID: pgtype.Int8{Int64: parent.ID, Valid: true},
			Kind: kindFolder, NameToken: token, EncryptedMetadata: metadataData, MetadataSignature: metadataSignature,
		})
		if err != nil {
			return err
		}
		if err = queries.CreateNodeKeyEnvelope(ctx, db.CreateNodeKeyEnvelopeParams{
			ChildNodeID: nodeID, ParentNodeID: parent.ID, LibraryID: library.ID, ChildEpoch: 1,
			ParentEpoch: parent.KeyEpoch, Generation: 1, Ciphertext: envelopeData, OwnerSignature: envelopeSignature,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.FolderCreated, ActorID: o.id, TargetID: metadata.Fields["node_id"],
			Details: audit.Details{Encrypted: true, LibraryID: library.PublicID},
		})
	})
	if err != nil {
		return nil, s.result(ctx, "create encrypted folder", err)
	}
	return s.nodeView(ctx, nodeID)
}

func (s *Service) listNodes(ctx context.Context, input *listEncryptedNodesInput) (*nodesOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	library, err := s.queries.GetV2LibraryByPublicIDAndOwner(ctx, db.GetV2LibraryByPublicIDAndOwnerParams{
		PublicID: input.LibraryID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get encrypted library for listing", err)
	}
	parentID, parentPublicID := library.RootNodeID, library.RootNodePublicID
	if input.ParentID != "" {
		parent, loadErr := s.activeFolder(ctx, s.queries, input.ParentID, user.ID, library.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		parentID, parentPublicID = parent.ID, parent.PublicID
	}
	afterID := int64(0)
	if input.Cursor != "" {
		cursor, loadErr := s.queries.GetV2NodeByPublicIDAndOwner(ctx, db.GetV2NodeByPublicIDAndOwnerParams{
			PublicID: input.Cursor, OwnerID: user.ID,
		})
		if errors.Is(loadErr, pgx.ErrNoRows) || loadErr == nil && (cursor.LibraryID != library.ID || cursor.ParentID.Int64 != parentID) {
			return nil, huma.Error422UnprocessableEntity("The cursor is not valid for this folder.")
		}
		if loadErr != nil {
			return nil, s.internalError(ctx, "get encrypted folder cursor", loadErr)
		}
		afterID = cursor.ID
	}
	limit := input.Limit
	if limit == 0 {
		limit = pageSize
	}
	rows, err := s.queries.ListV2ChildNodes(ctx, db.ListV2ChildNodesParams{
		LibraryID: library.ID, ParentID: pgtype.Int8{Int64: parentID, Valid: true}, AfterID: afterID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, s.internalError(ctx, "list encrypted folder", err)
	}
	output := &nodesOutput{Body: encryptedNodesPage{Items: make([]EncryptedNode, 0, min(len(rows), int(limit)))}}
	if len(rows) > int(limit) {
		output.Body.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		output.Body.Items = append(output.Body.Items, nodeFromView(db.GetV2NodeViewRow{
			ID: row.ID, PublicID: row.PublicID, Kind: row.Kind, NameToken: row.NameToken,
			EncryptedMetadata: row.EncryptedMetadata, MetadataSignature: row.MetadataSignature, KeyEpoch: row.KeyEpoch,
			MetadataRevision: row.MetadataRevision, Revision: row.Revision,
			CurrentVersionPointer: row.CurrentVersionPointer, CurrentVersionSignature: row.CurrentVersionSignature,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ParentPublicID: parentPublicID,
			ParentEnvelope: row.ParentEnvelope, ParentEnvelopeSignature: row.ParentEnvelopeSignature,
		}))
	}
	return output, nil
}

func (s *Service) renameNode(ctx context.Context, input *renameEncryptedNodeInput) (*nodeOutput, error) {
	o, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	metadata, data, signature, err := o.signed("metadata", "node-metadata", input.Body.Metadata)
	if err != nil {
		return nil, err
	}
	token, err := decodeNameToken(input.Body.NameToken)
	if err != nil {
		return nil, err
	}
	var nodeID int64
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		node, err := queries.LockV2NodeByPublicIDAndOwner(ctx, db.LockV2NodeByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: o.id})
		if err != nil {
			return err
		}
		if !node.ParentID.Valid {
			return huma.Error422UnprocessableEntity("Replace the library metadata to rename the library.")
		}
		if node.TrashedAt.Valid {
			return pgx.ErrNoRows
		}
		if input.IfMatch != quoted(node.MetadataRevision) {
			return huma.Error409Conflict("The item changed. Load it again.")
		}
		revision := node.MetadataRevision + 1
		if err = expect("metadata", metadata, "library_id", node.LibraryPublicID, "node_id", node.PublicID,
			"node_epoch", counter(node.KeyEpoch), "revision", counter(revision)); err != nil {
			return err
		}
		if err = queries.UpdateV2NodeMetadata(ctx, db.UpdateV2NodeMetadataParams{
			EncryptedMetadata: data, MetadataSignature: signature, MetadataRevision: revision, NameToken: token, ID: node.ID,
		}); err != nil {
			return err
		}
		nodeID = node.ID
		event := audit.Event{
			Action: audit.FolderRenamed, TargetID: node.PublicID,
			Details: audit.Details{Encrypted: true, LibraryID: node.LibraryPublicID},
		}
		if node.Kind == kindFile {
			if event, err = audit.FileEvent(ctx, queries, audit.FileRenamed, node.ID); err != nil {
				return err
			}
		}
		event.ActorID = o.id
		return audit.Record(ctx, queries, event)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The file or folder does not exist.")
	}
	if err != nil {
		return nil, s.result(ctx, "rename encrypted node", err)
	}
	return s.nodeView(ctx, nodeID)
}

func (s *Service) activeFolder(ctx context.Context, queries *db.Queries, publicID string, ownerID, libraryID int64) (db.GetV2NodeByPublicIDAndOwnerRow, error) {
	folder, err := queries.GetV2NodeByPublicIDAndOwner(ctx, db.GetV2NodeByPublicIDAndOwnerParams{PublicID: publicID, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (folder.LibraryID != libraryID || folder.Kind != kindFolder || folder.TrashedAt.Valid) {
		return folder, huma.Error404NotFound("The folder does not exist.")
	}
	if err != nil {
		return folder, s.internalError(ctx, "get encrypted folder", err)
	}
	return folder, nil
}

func (s *Service) nodeView(ctx context.Context, nodeID int64) (*nodeOutput, error) {
	row, err := s.queries.GetV2NodeView(ctx, nodeID)
	if err != nil {
		return nil, s.internalError(ctx, "get encrypted node", err)
	}
	return &nodeOutput{Body: nodeFromView(row)}, nil
}

func nodeFromView(row db.GetV2NodeViewRow) EncryptedNode {
	node := EncryptedNode{
		ID: row.PublicID, ParentID: row.ParentPublicID, Kind: row.Kind, KeyEpoch: counter(row.KeyEpoch),
		MetadataRevision: counter(row.MetadataRevision), Revision: counter(row.Revision),
		NameToken:      base64.RawURLEncoding.EncodeToString(row.NameToken),
		Metadata:       encryption.Encode(row.EncryptedMetadata, row.MetadataSignature),
		ParentEnvelope: encryption.Encode(row.ParentEnvelope, row.ParentEnvelopeSignature),
		CreatedAt:      row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if len(row.CurrentVersionPointer) > 0 {
		current := encryption.Encode(row.CurrentVersionPointer, row.CurrentVersionSignature)
		node.CurrentVersion = &current
	}
	return node
}

func decodeNameToken(value string) ([]byte, error) {
	token, err := encryptionv2.DecodeBase64URL(value, 32)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("Send a 32-byte name token in unpadded base64url.")
	}
	return token, nil
}

func counter(value int64) string { return strconv.FormatInt(value, 10) }

func quoted(value int64) string { return `"` + counter(value) + `"` }

func libraryNotFound() error { return huma.Error404NotFound("The library does not exist.") }

// result keeps client errors from inside a transaction and hides other errors.
func (s *Service) result(ctx context.Context, operation string, err error) error {
	if statusErr, ok := errors.AsType[huma.StatusError](err); ok {
		return statusErr
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "nodes_encrypted_active_name_key" {
			return huma.Error409Conflict("A file or folder with this name already exists.")
		}
		return huma.Error409Conflict("An item with this identifier already exists.")
	}
	return s.internalError(ctx, operation, err)
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Encrypted storage is unavailable.")
}
