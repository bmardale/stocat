package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/quota"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const (
	EncryptionNone = "none"
	EncryptionE2EE = "e2ee"
	FormatE2EEV1   = "stocat-framed-v1"

	stateCreated    = "created"
	stateUploading  = "uploading"
	stateUploaded   = "uploaded"
	stateFinalizing = "finalizing"
	stateCompleted  = "completed"

	jobFinalize = "finalize-upload"
	tusVersion  = "1.0.0"
)

type Config struct {
	StagingDir      string
	MaxUploadSize   int64
	StagingCapacity int64
	SessionLifetime time.Duration
	Logger          *slog.Logger
}

type Service struct {
	api      huma.API
	pool     *pgxpool.Pool
	queries  *db.Queries
	staging  *os.Root
	maxSize  int64
	capacity int64
	lifetime time.Duration
	log      *slog.Logger
	queue    *river.Client[pgx.Tx]
	locksMu  sync.Mutex
	locks    map[int64]*sync.Mutex
}

type Upload struct {
	ID             string    `json:"id"`
	State          string    `json:"state"`
	UploadURL      string    `json:"upload_url"`
	DeclaredSize   int64     `json:"declared_size"`
	Offset         int64     `json:"offset"`
	ExpiresAt      time.Time `json:"expires_at"`
	NodeID         string    `json:"node_id,omitempty"`
	VersionID      string    `json:"version_id,omitempty"`
	FailureCode    string    `json:"failure_code,omitempty"`
	FailureMessage string    `json:"failure_message,omitempty"`
}

type createInput struct {
	Body struct {
		LibraryID        string `json:"library_id" maxLength:"64"`
		ParentID         string `json:"parent_id,omitempty" maxLength:"64"`
		TargetNodeID     string `json:"target_node_id,omitempty" maxLength:"64"`
		ExpectedRevision int64  `json:"expected_revision,omitempty" minimum:"0"`
		Name             string `json:"name,omitempty" maxLength:"255"`
		EncryptedName    []byte `json:"encrypted_name,omitempty"`
		NameToken        []byte `json:"name_token,omitempty"`
		Size             int64  `json:"size" minimum:"0"`
	}
}

type uploadInput struct {
	ID string `path:"id" maxLength:"64"`
}
type completeInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		DedupFingerprint []byte `json:"dedup_fingerprint,omitempty"`
		EncryptionFormat string `json:"encryption_format,omitempty" maxLength:"64"`
		EncryptedFileKey []byte `json:"encrypted_file_key,omitempty"`
	}
}
type uploadOutput struct{ Body Upload }
type noContentOutput struct {
	Status int `status:"204"`
}

func New(pool *pgxpool.Pool, queue *river.Client[pgx.Tx], cfg Config) (*Service, error) {
	if cfg.StagingDir == "" {
		cfg.StagingDir = filepath.Join(os.TempDir(), "stocat-uploads")
	}
	if cfg.MaxUploadSize <= 0 {
		cfg.MaxUploadSize = 10 << 30
	}
	if cfg.StagingCapacity <= 0 {
		cfg.StagingCapacity = 20 << 30
	}
	if cfg.SessionLifetime <= 0 {
		cfg.SessionLifetime = 24 * time.Hour
	}
	if err := os.MkdirAll(cfg.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create upload staging directory: %w", err)
	}
	root, err := os.OpenRoot(cfg.StagingDir)
	if err != nil {
		return nil, fmt.Errorf("open upload staging directory: %w", err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		pool: pool, queries: db.New(pool), staging: root,
		maxSize: cfg.MaxUploadSize, capacity: cfg.StagingCapacity, lifetime: cfg.SessionLifetime,
		log: log, queue: queue, locks: make(map[int64]*sync.Mutex),
	}, nil
}

func (s *Service) Close() error { return s.staging.Close() }

func (s *Service) Register(api huma.API) {
	s.api = api
	group := huma.NewGroup(api, "/uploads")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Uploads"} })
	huma.Register(group, huma.Operation{
		OperationID: "uploads-create", Method: http.MethodPost, Path: "", Summary: "Create an upload session",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusInsufficientStorage},
	}, s.create)
	huma.Register(group, huma.Operation{
		OperationID: "uploads-get", Method: http.MethodGet, Path: "/{id}", Summary: "Get an upload session",
		Errors: []int{http.StatusNotFound},
	}, s.get)
	huma.Register(group, huma.Operation{
		OperationID: "uploads-complete", Method: http.MethodPost, Path: "/{id}/complete", Summary: "Complete an upload transfer",
		MaxBodyBytes: 65536, Errors: []int{http.StatusConflict, http.StatusNotFound},
	}, s.complete)
	huma.Register(group, huma.Operation{
		OperationID: "uploads-cancel", Method: http.MethodDelete, Path: "/{id}", Summary: "Cancel an upload session",
		DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusConflict, http.StatusNotFound},
	}, s.cancel)
	s.registerTus(group)
}

func (s *Service) create(ctx context.Context, input *createInput) (*uploadOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	if input.Body.Size > s.maxSize {
		return nil, huma.Error413RequestEntityTooLarge("The file exceeds the upload limit.")
	}
	var session db.UploadSession
	var backendName string
	err := db.InTx(ctx, s.pool, func(q *db.Queries) error {
		if err := q.LockUploadAdmission(ctx); err != nil {
			return err
		}
		library, err := q.LockLibraryForUpload(ctx, db.LockLibraryForUploadParams{PublicID: input.Body.LibraryID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		parentID := library.RootNodeID
		if input.Body.ParentID != "" {
			parent, loadErr := q.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{PublicID: input.Body.ParentID, OwnerID: user.ID})
			if loadErr != nil || parent.LibraryID != library.ID || parent.Kind != "folder" || parent.TrashedAt.Valid {
				return pgx.ErrNoRows
			}
			parentID = parent.ID
		}
		var targetID, revision pgtype.Int8
		var name pgtype.Text
		var encryptedName, nameToken []byte
		if input.Body.TargetNodeID != "" {
			target, loadErr := q.GetNodeByPublicIDAndOwner(ctx, db.GetNodeByPublicIDAndOwnerParams{PublicID: input.Body.TargetNodeID, OwnerID: user.ID})
			if loadErr != nil || target.LibraryID != library.ID || target.Kind != "file" || target.TrashedAt.Valid || input.Body.ExpectedRevision <= 0 {
				return errInvalidTarget
			}
			targetID, revision = validInt(target.ID), validInt(input.Body.ExpectedRevision)
			parentID = target.ParentID.Int64
			name, encryptedName, nameToken = target.Name, target.EncryptedName, target.NameToken
		} else {
			name, encryptedName, nameToken, err = validateName(library.EncryptionMode, input.Body.Name, input.Body.EncryptedName, input.Body.NameToken)
			if err != nil {
				return err
			}
		}
		backendName = library.BackendName
		if err := quota.Admit(ctx, q, user.ID, library.BackendID, input.Body.Size); err != nil {
			return err
		}
		allReserved, err := q.SumAllUploadReservations(ctx)
		if err != nil {
			return err
		}
		if exceeds(0, allReserved, input.Body.Size, s.capacity) {
			return errCapacity
		}
		publicID := id.New(id.Upload)
		session, err = q.CreateUploadSession(ctx, db.CreateUploadSessionParams{
			PublicID: publicID, OwnerID: user.ID, LibraryID: library.ID, ParentID: parentID,
			TargetNodeID: targetID, ExpectedRevision: revision, Name: name, EncryptedName: encryptedName,
			NameToken: nameToken, DeclaredSize: input.Body.Size, StagingKey: publicID + ".part",
			DestinationKey: "objects/" + library.PublicID + "/" + publicID,
			ExpiresAt:      pgtype.Timestamptz{Time: time.Now().Add(s.lifetime), Valid: true},
		})
		if err == nil && input.Body.Size == 0 {
			err = q.SetZeroLengthUploadReady(ctx, session.ID)
			session.State = stateUploaded
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The library or folder does not exist.")
	}
	if errors.Is(err, errInvalidTarget) {
		return nil, huma.Error422UnprocessableEntity("Select a current file revision to replace.")
	}
	if errors.Is(err, quota.ErrExceeded) {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("The upload exceeds your storage quota on %s.", backendName))
	}
	if errors.Is(err, errCapacity) {
		return nil, huma.NewError(http.StatusInsufficientStorage, "The upload staging area is full.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "create upload", err)
	}
	file, err := s.staging.OpenFile(session.StagingKey, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = s.queries.MarkUploadFailed(ctx, db.MarkUploadFailedParams{ID: session.ID, FailureCode: textValue("staging"), FailureMessage: textValue("Create the staging file again.")})
		return nil, huma.Error503ServiceUnavailable("Upload staging is unavailable.")
	}
	if err := file.Close(); err != nil {
		_ = s.queries.MarkUploadFailed(ctx, db.MarkUploadFailedParams{ID: session.ID, FailureCode: textValue("staging"), FailureMessage: textValue("Create the staging file again.")})
		return nil, huma.Error503ServiceUnavailable("Upload staging is unavailable.")
	}
	if session.DeclaredSize == 0 && len(session.EncryptedName) == 0 {
		if err := s.startFinalization(ctx, session.ID, nil); err != nil {
			return nil, s.databaseError(ctx, "enqueue empty upload", err)
		}
		session.State = stateFinalizing
	}
	return &uploadOutput{Body: uploadFromRow(session)}, nil
}

func (s *Service) get(ctx context.Context, input *uploadInput) (*uploadOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetUploadSessionByPublicIDAndOwner(ctx, db.GetUploadSessionByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The upload session does not exist.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "get upload", err)
	}
	output := uploadFromRow(row)
	if row.PublishedNodeID.Valid {
		published, loadErr := s.queries.GetPublishedUploadIDs(ctx, row.ID)
		if loadErr != nil {
			return nil, s.databaseError(ctx, "get published upload identifiers", loadErr)
		}
		output.NodeID = published.NodePublicID
		output.VersionID = published.VersionPublicID
	}
	return &uploadOutput{Body: output}, nil
}

func (s *Service) complete(ctx context.Context, input *completeInput) (*uploadOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetUploadSessionByPublicIDAndOwner(ctx, db.GetUploadSessionByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The upload session does not exist.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "get upload for completion", err)
	}
	if row.State != stateUploaded {
		if row.State == stateFinalizing || row.State == stateCompleted {
			return &uploadOutput{Body: uploadFromRow(row)}, nil
		}
		return nil, huma.Error409Conflict("Upload all bytes before completion.")
	}
	publication, err := s.queries.GetUploadPublication(ctx, row.ID)
	if err != nil {
		return nil, s.databaseError(ctx, "get upload encryption mode", err)
	}
	if publication.EncryptionMode == EncryptionNone {
		if len(input.Body.DedupFingerprint) != 0 || input.Body.EncryptionFormat != "" || len(input.Body.EncryptedFileKey) != 0 {
			return nil, huma.Error422UnprocessableEntity("Do not send encryption metadata for an unencrypted upload.")
		}
		if err := s.startFinalization(ctx, row.ID, nil); errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error409Conflict("The upload state changed.")
		} else if err != nil {
			return nil, s.databaseError(ctx, "complete upload", err)
		}
		row.State = stateFinalizing
		return &uploadOutput{Body: uploadFromRow(row)}, nil
	}
	if len(input.Body.DedupFingerprint) != 32 || input.Body.EncryptionFormat != FormatE2EEV1 || len(input.Body.EncryptedFileKey) == 0 {
		return nil, huma.Error422UnprocessableEntity("Send the encrypted file key, format, and 32-byte fingerprint.")
	}
	metadata := &encryptedMetadata{
		DedupFingerprint: input.Body.DedupFingerprint,
		EncryptionFormat: input.Body.EncryptionFormat,
		EncryptedFileKey: input.Body.EncryptedFileKey,
	}
	if err := s.startFinalization(ctx, row.ID, metadata); errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error409Conflict("The upload state changed.")
	} else if err != nil {
		return nil, s.databaseError(ctx, "complete encrypted upload", err)
	}
	row.State = stateFinalizing
	row.DedupFingerprint = metadata.DedupFingerprint
	row.EncryptionFormat = textValue(metadata.EncryptionFormat)
	row.EncryptedFileKey = metadata.EncryptedFileKey
	return &uploadOutput{Body: uploadFromRow(row)}, nil
}

func (s *Service) cancel(ctx context.Context, input *uploadInput) (*noContentOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.CancelUploadSession(ctx, db.CancelUploadSessionParams{PublicID: input.ID, OwnerID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		current, loadErr := s.queries.GetUploadSessionByPublicIDAndOwner(ctx, db.GetUploadSessionByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("The upload session does not exist.")
		}
		if loadErr != nil {
			return nil, s.databaseError(ctx, "get upload for cancellation", loadErr)
		}
		return nil, huma.Error409Conflict("The " + current.State + " upload cannot be cancelled.")
	}
	if err != nil {
		return nil, s.databaseError(ctx, "cancel upload", err)
	}
	_ = s.staging.Remove(row.StagingKey)
	return &noContentOutput{Status: http.StatusNoContent}, nil
}

type encryptedMetadata struct {
	DedupFingerprint []byte
	EncryptionFormat string
	EncryptedFileKey []byte
}

func (s *Service) startFinalization(ctx context.Context, uploadID int64, metadata *encryptedMetadata) error {
	if s.queue == nil {
		return fmt.Errorf("publication queue is unavailable")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		var err error
		if metadata == nil {
			_, err = q.SetPlainUploadFinalizing(ctx, uploadID)
		} else {
			_, err = q.SetUploadFinalizing(ctx, db.SetUploadFinalizingParams{
				ID: uploadID, DedupFingerprint: metadata.DedupFingerprint,
				EncryptionFormat: textValue(metadata.EncryptionFormat), EncryptedFileKey: metadata.EncryptedFileKey,
			})
		}
		if err != nil {
			return err
		}
		_, err = s.queue.InsertTx(ctx, tx, FinalizeArgs{UploadID: uploadID}, nil)
		return err
	})
}

func validateName(mode, plain string, encrypted, token []byte) (pgtype.Text, []byte, []byte, error) {
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

func uploadFromRow(row db.UploadSession) Upload {
	result := Upload{
		ID: row.PublicID, State: row.State, UploadURL: "/api/v1/uploads/" + row.PublicID + "/content",
		DeclaredSize: row.DeclaredSize, Offset: row.UploadOffset, ExpiresAt: row.ExpiresAt.Time,
	}
	if row.FailureCode.Valid {
		result.FailureCode = row.FailureCode.String
	}
	if row.FailureMessage.Valid {
		result.FailureMessage = row.FailureMessage.String
	}
	return result
}

func validInt(value int64) pgtype.Int8   { return pgtype.Int8{Int64: value, Valid: true} }
func textValue(value string) pgtype.Text { return pgtype.Text{String: value, Valid: true} }
func exceeds(first, second, add, limit int64) bool {
	return add > limit || first > limit-add || second > limit-add-first
}

var (
	errInvalidTarget = errors.New("invalid upload target")
	errCapacity      = errors.New("upload staging capacity exceeded")
)

func (s *Service) databaseError(ctx context.Context, operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return huma.Error409Conflict("A file or folder with this name already exists.")
	}
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Uploads are unavailable.")
}

func (s *Service) sessionLock(id int64) func() {
	s.locksMu.Lock()
	lock := s.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[id] = lock
	}
	s.locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func parseOffset(value string) (int64, error) {
	offset, err := strconv.ParseInt(value, 10, 64)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid upload offset")
	}
	return offset, nil
}

func copyPatch(dst io.Writer, src io.Reader, length int64) error {
	written, err := io.CopyN(dst, src, length)
	if err != nil || written != length {
		return fmt.Errorf("read upload patch: %w", err)
	}
	var extra [1]byte
	if n, _ := src.Read(extra[:]); n != 0 {
		return fmt.Errorf("upload patch exceeds Content-Length")
	}
	return nil
}
