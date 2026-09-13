// Package libraries manages private file libraries and folders.
package libraries

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EncryptionNone = "none"
	EncryptionE2EE = "e2ee"
	nodeFolder     = "folder"
	pageSize       = 100
)

type LibraryBackend struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type" enum:"local,s3"`
}

type Library struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	EncryptionMode string         `json:"encryption_mode" enum:"none,e2ee"`
	KeyEnvelope    []byte         `json:"key_envelope,omitempty"`
	RootNodeID     string         `json:"root_node_id"`
	Backend        LibraryBackend `json:"backend"`
	QuotaMB        *int64         `json:"quota_mb" doc:"Quota override in megabytes. A null value uses the user default quota."`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type Node struct {
	ID            string    `json:"id"`
	LibraryID     string    `json:"library_id"`
	ParentID      string    `json:"parent_id,omitempty"`
	Kind          string    `json:"kind" enum:"file,folder"`
	Name          string    `json:"name,omitempty"`
	EncryptedName []byte    `json:"encrypted_name,omitempty"`
	NameToken     []byte    `json:"name_token,omitempty"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	log     *slog.Logger
}

type createLibraryInput struct {
	Body struct {
		Name           string `json:"name" minLength:"1" maxLength:"100"`
		BackendID      string `json:"backend_id" minLength:"1" maxLength:"64"`
		EncryptionMode string `json:"encryption_mode" enum:"none,e2ee"`
		KeyEnvelope    []byte `json:"key_envelope,omitempty"`
	}
}

type libraryInput struct {
	ID string `path:"id" maxLength:"64"`
}

type folderInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	Body      struct {
		ParentID      string `json:"parent_id,omitempty" maxLength:"64"`
		Name          string `json:"name,omitempty" maxLength:"255"`
		EncryptedName []byte `json:"encrypted_name,omitempty"`
		NameToken     []byte `json:"name_token,omitempty"`
	}
}

type deleteFolderInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	FolderID  string `path:"folder_id" maxLength:"64"`
}

type listNodesInput struct {
	LibraryID string `path:"id" maxLength:"64"`
	ParentID  string `query:"parent_id" maxLength:"64"`
	Cursor    string `query:"cursor" maxLength:"64"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
}

type backendsOutput struct {
	Body []LibraryBackend `nullable:"false"`
}
type libraryOutput struct{ Body Library }
type librariesOutput struct {
	Body []Library `nullable:"false"`
}
type nodeOutput struct{ Body Node }
type nodesPage struct {
	Items      []Node `json:"items" nullable:"false"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type nodesOutput struct{ Body nodesPage }

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), log: logger}
}

func (s *Service) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "library-backends-list", Method: http.MethodGet, Path: "/storage-backends",
		Summary: "List storage backends for new libraries", Tags: []string{"Libraries"},
	}, s.listBackends)
	group := huma.NewGroup(api, "/libraries")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Libraries"} })
	huma.Register(group, huma.Operation{
		OperationID: "libraries-list", Method: http.MethodGet, Path: "", Summary: "List private libraries",
	}, s.list)
	huma.Register(group, huma.Operation{
		OperationID: "libraries-create", Method: http.MethodPost, Path: "", Summary: "Create a private library",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.create)
	huma.Register(group, huma.Operation{
		OperationID: "libraries-get", Method: http.MethodGet, Path: "/{id}", Summary: "Get a private library",
		Errors: []int{http.StatusNotFound},
	}, s.get)
	huma.Register(group, huma.Operation{
		OperationID: "folders-create", Method: http.MethodPost, Path: "/{id}/folders", Summary: "Create a folder",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.createFolder)
	huma.Register(group, huma.Operation{
		OperationID: "nodes-list", Method: http.MethodGet, Path: "/{id}/nodes", Summary: "List a folder",
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.listNodes)
	huma.Register(group, huma.Operation{
		OperationID: "folders-delete", Method: http.MethodDelete, Path: "/{id}/folders/{folder_id}",
		Summary:       "Move a folder to the trash",
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusConflict, http.StatusNotFound},
	}, s.deleteFolder)
}

func (s *Service) listBackends(ctx context.Context, _ *struct{}) (*backendsOutput, error) {
	rows, err := s.queries.ListEnabledStorageBackends(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list enabled storage backends", err)
	}
	output := &backendsOutput{Body: make([]LibraryBackend, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, LibraryBackend{ID: row.PublicID, Name: row.Name, Type: row.Type})
	}
	return output, nil
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*librariesOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	rows, err := s.queries.ListLibrariesByOwner(ctx, user.ID)
	if err != nil {
		return nil, s.internalError(ctx, "list libraries", err)
	}
	output := &librariesOutput{Body: make([]Library, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, libraryFromListRow(row))
	}
	return output, nil
}

func (s *Service) get(ctx context.Context, input *libraryInput) (*libraryOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetLibraryByPublicIDAndOwner(ctx, db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: input.ID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get library", err)
	}
	return &libraryOutput{Body: libraryFromGetRow(row)}, nil
}

func (s *Service) create(ctx context.Context, input *createLibraryInput) (*libraryOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a library name.")
	}
	if err := validateEncryption(input.Body.EncryptionMode, input.Body.KeyEnvelope); err != nil {
		return nil, err
	}
	publicID := id.New(id.Library)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		backend, err := queries.GetEnabledBackendByPublicID(ctx, input.Body.BackendID)
		if err != nil {
			return err
		}
		libraryID, err := queries.NextLibraryID(ctx)
		if err != nil {
			return err
		}
		rootID, err := queries.NextNodeID(ctx)
		if err != nil {
			return err
		}
		_, err = queries.CreateLibrary(ctx, db.CreateLibraryParams{
			ID: libraryID, PublicID: publicID, OwnerID: user.ID, BackendID: backend.ID, RootNodeID: rootID,
			Name: name, EncryptionMode: input.Body.EncryptionMode, KeyEnvelope: input.Body.KeyEnvelope,
		})
		if err != nil {
			return err
		}
		_, err = queries.CreateNodeWithID(ctx, db.CreateNodeWithIDParams{
			ID: rootID, PublicID: id.New(id.Node), LibraryID: libraryID, Kind: nodeFolder,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The enabled storage backend does not exist.")
	}
	if isConstraint(err, "libraries_owner_id_name_key") {
		return nil, huma.Error409Conflict("A library with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create library", err)
	}
	return s.get(ctx, &libraryInput{ID: publicID})
}

func (s *Service) createFolder(ctx context.Context, input *folderInput) (*nodeOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	library, err := s.queries.GetLibraryByPublicIDAndOwner(ctx, db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: input.LibraryID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get library for folder", err)
	}
	parentID := library.RootNodeID
	parentPublicID := library.RootNodePublicID
	if input.Body.ParentID != "" {
		parent, loadErr := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
			PublicID: input.Body.ParentID, OwnerID: user.ID,
		})
		if errors.Is(loadErr, pgx.ErrNoRows) || loadErr == nil && (parent.LibraryID != library.ID || parent.Kind != nodeFolder || parent.TrashedAt.Valid) {
			return nil, huma.Error404NotFound("The parent folder does not exist.")
		}
		if loadErr != nil {
			return nil, s.internalError(ctx, "get parent folder", loadErr)
		}
		parentID, parentPublicID = parent.ID, parent.PublicID
	}
	name, encryptedName, nameToken, err := ValidateNodeName(library.EncryptionMode, input.Body.Name, input.Body.EncryptedName, input.Body.NameToken)
	if err != nil {
		return nil, err
	}
	row, err := s.queries.CreateNode(ctx, db.CreateNodeParams{
		PublicID: id.New(id.Node), LibraryID: library.ID, ParentID: pgtype.Int8{Int64: parentID, Valid: true},
		Kind: nodeFolder, Name: name, EncryptedName: encryptedName, NameToken: nameToken,
	})
	if isConstraint(err, "nodes_plain_active_name_key") || isConstraint(err, "nodes_encrypted_active_name_key") {
		return nil, huma.Error409Conflict("A file or folder with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create folder", err)
	}
	return &nodeOutput{Body: NodeFromRow(row, library.PublicID, parentPublicID)}, nil
}

func (s *Service) deleteFolder(ctx context.Context, input *deleteFolderInput) (*struct{}, error) {
	user, _ := auth.UserFromContext(ctx)
	library, err := s.queries.GetLibraryByPublicIDAndOwner(ctx, db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: input.LibraryID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get library for folder deletion", err)
	}
	node, err := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
		PublicID: input.FolderID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (node.LibraryID != library.ID || node.Kind != nodeFolder || !node.ParentID.Valid || node.TrashedAt.Valid) {
		return nil, huma.Error404NotFound("The folder does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "get folder for deletion", err)
	}
	blocked, err := s.queries.CountActiveUploadSessionsInSubtree(ctx, node.ID)
	if err != nil {
		return nil, s.internalError(ctx, "count uploads in folder", err)
	}
	if blocked > 0 {
		return nil, huma.Error409Conflict("An upload is in progress in this folder. Cancel the upload and try again.")
	}
	if _, err := s.queries.TrashNodeSubtree(ctx, node.ID); err != nil {
		return nil, s.internalError(ctx, "trash folder", err)
	}
	return nil, nil
}

func (s *Service) listNodes(ctx context.Context, input *listNodesInput) (*nodesOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	library, err := s.queries.GetLibraryByPublicIDAndOwner(ctx, db.GetLibraryByPublicIDAndOwnerParams{
		PublicID: input.LibraryID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, libraryNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get library for listing", err)
	}
	parentID, parentPublicID := library.RootNodeID, library.RootNodePublicID
	if input.ParentID != "" {
		parent, loadErr := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
			PublicID: input.ParentID, OwnerID: user.ID,
		})
		if errors.Is(loadErr, pgx.ErrNoRows) || loadErr == nil && (parent.LibraryID != library.ID || parent.Kind != nodeFolder || parent.TrashedAt.Valid) {
			return nil, huma.Error404NotFound("The folder does not exist.")
		}
		if loadErr != nil {
			return nil, s.internalError(ctx, "get folder", loadErr)
		}
		parentID, parentPublicID = parent.ID, parent.PublicID
	}
	afterID := int64(0)
	if input.Cursor != "" {
		cursor, loadErr := s.queries.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{
			PublicID: input.Cursor, OwnerID: user.ID,
		})
		if errors.Is(loadErr, pgx.ErrNoRows) || loadErr == nil && (cursor.LibraryID != library.ID || !cursor.ParentID.Valid || cursor.ParentID.Int64 != parentID) {
			return nil, huma.Error422UnprocessableEntity("The cursor is not valid for this folder.")
		}
		if loadErr != nil {
			return nil, s.internalError(ctx, "get folder cursor", loadErr)
		}
		afterID = cursor.ID
	}
	limit := input.Limit
	if limit == 0 {
		limit = pageSize
	}
	rows, err := s.queries.ListChildNodes(ctx, db.ListChildNodesParams{
		LibraryID: library.ID, ParentID: pgtype.Int8{Int64: parentID, Valid: true}, AfterID: afterID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, s.internalError(ctx, "list folder", err)
	}
	output := &nodesOutput{Body: nodesPage{Items: make([]Node, 0, min(len(rows), int(limit)))}}
	if len(rows) > int(limit) {
		output.Body.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		output.Body.Items = append(output.Body.Items, NodeFromRow(row, library.PublicID, parentPublicID))
	}
	return output, nil
}

func validateEncryption(mode string, keyEnvelope []byte) error {
	switch mode {
	case EncryptionNone:
		if len(keyEnvelope) != 0 {
			return huma.Error422UnprocessableEntity("Do not send a key envelope for an unencrypted library.")
		}
	case EncryptionE2EE:
		if len(keyEnvelope) == 0 {
			return huma.Error422UnprocessableEntity("Send the encrypted library key.")
		}
	default:
		return huma.Error422UnprocessableEntity("Use a supported encryption mode.")
	}
	return nil
}

func ValidateNodeName(mode, plain string, encrypted, token []byte) (pgtype.Text, []byte, []byte, error) {
	if mode == EncryptionE2EE {
		if plain != "" || len(encrypted) == 0 || len(token) != 32 {
			return pgtype.Text{}, nil, nil, huma.Error422UnprocessableEntity("Send an encrypted name and a 32-byte name token.")
		}
		return pgtype.Text{}, encrypted, token, nil
	}
	if len(encrypted) != 0 || len(token) != 0 {
		return pgtype.Text{}, nil, nil, huma.Error422UnprocessableEntity("Send a plain name for an unencrypted library.")
	}
	name := strings.TrimSpace(plain)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return pgtype.Text{}, nil, nil, huma.Error422UnprocessableEntity("Enter a name without separators, NUL, dot, or dot-dot.")
	}
	return pgtype.Text{String: name, Valid: true}, nil, nil, nil
}

func libraryFromListRow(row db.ListLibrariesByOwnerRow) Library {
	return Library{
		ID: row.PublicID, Name: row.Name, EncryptionMode: row.EncryptionMode, KeyEnvelope: row.KeyEnvelope,
		RootNodeID: row.RootNodePublicID,
		Backend:    LibraryBackend{ID: row.BackendPublicID, Name: row.BackendName, Type: row.BackendType},
		QuotaMB:    quotaPtr(row.QuotaMb),
		CreatedAt:  row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func libraryFromGetRow(row db.GetLibraryByPublicIDAndOwnerRow) Library {
	return Library{
		ID: row.PublicID, Name: row.Name, EncryptionMode: row.EncryptionMode, KeyEnvelope: row.KeyEnvelope,
		RootNodeID: row.RootNodePublicID,
		Backend:    LibraryBackend{ID: row.BackendPublicID, Name: row.BackendName, Type: row.BackendType},
		QuotaMB:    quotaPtr(row.QuotaMb),
		CreatedAt:  row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func quotaPtr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	quota := value.Int64
	return &quota
}

func NodeFromRow(row db.Node, libraryID, parentID string) Node {
	node := Node{
		ID: row.PublicID, LibraryID: libraryID, ParentID: parentID, Kind: row.Kind,
		EncryptedName: row.EncryptedName, NameToken: row.NameToken, Revision: row.Revision,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.Name.Valid {
		node.Name = row.Name.String
	}
	return node
}

func libraryNotFound() error { return huma.Error404NotFound("The library does not exist.") }

func isConstraint(err error, name string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505" && pgErr.ConstraintName == name
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("File storage is unavailable.")
}
