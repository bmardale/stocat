package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/crypt"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Encrypter *crypt.Encrypter
	Logger    *slog.Logger
}

type Service struct {
	pool      *pgxpool.Pool
	queries   *db.Queries
	encrypter *crypt.Encrypter
	log       *slog.Logger
}

type backendInput struct {
	ID string `path:"id" maxLength:"64"`
}

type createBackendInput struct {
	Body struct {
		Name    string       `json:"name" minLength:"1" maxLength:"100"`
		Type    string       `json:"type" enum:"local,s3"`
		Enabled bool         `json:"enabled"`
		Local   *LocalConfig `json:"local,omitempty" doc:"Required for local backends."`
		S3      *S3Input     `json:"s3,omitempty" doc:"Required for S3 backends."`
	}
}

type updateBackendInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Name    string       `json:"name" minLength:"1" maxLength:"100"`
		Enabled bool         `json:"enabled"`
		Local   *LocalConfig `json:"local,omitempty" doc:"Required for local backends."`
		S3      *S3Input     `json:"s3,omitempty" doc:"Required for S3 backends."`
	}
}

type backendOutput struct {
	Body Backend
}

type backendsOutput struct {
	Body []Backend `nullable:"false"`
}

func New(pool *pgxpool.Pool, cfg Config) *Service {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), encrypter: cfg.Encrypter, log: log}
}

// Register adds the routes to api. The caller must restrict api to administrators.
func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/storage-backends")
	group.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Storage"}
	})
	group.UseTransformer(apierr.RedactValues)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-list", Method: http.MethodGet, Path: "",
		Summary: "List storage backends",
	}, s.list)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-create", Method: http.MethodPost, Path: "",
		Summary: "Create a storage backend", DefaultStatus: http.StatusCreated, MaxBodyBytes: 16384,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.create)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-get", Method: http.MethodGet, Path: "/{id}",
		Summary: "Get a storage backend", Errors: []int{http.StatusNotFound},
	}, s.get)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-update", Method: http.MethodPut, Path: "/{id}",
		Summary: "Update a storage backend", MaxBodyBytes: 16384,
		Description: "The backend type cannot change. Empty S3 credentials keep the stored credentials.",
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.update)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-delete", Method: http.MethodDelete, Path: "/{id}",
		Summary: "Delete a storage backend", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, s.delete)
	s.registerChecks(group)
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*backendsOutput, error) {
	rows, err := s.queries.ListStorageBackends(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list storage backends", err)
	}
	output := &backendsOutput{Body: make([]Backend, 0, len(rows))}
	for _, row := range rows {
		backend, err := backendFromRow(row)
		if err != nil {
			return nil, s.internalError(ctx, "list storage backends", err)
		}
		output.Body = append(output.Body, backend)
	}
	return output, nil
}

func (s *Service) get(ctx context.Context, input *backendInput) (*backendOutput, error) {
	row, err := s.queries.GetStorageBackendByPublicID(ctx, input.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get storage backend", err)
	}
	return s.output(ctx, row)
}

func (s *Service) create(ctx context.Context, input *createBackendInput) (*backendOutput, error) {
	name, err := validateName(input.Body.Name)
	if err != nil {
		return nil, err
	}
	settings, err := buildSettings(input.Body.Type, input.Body.Local, input.Body.S3, nil)
	if err != nil {
		return nil, s.requestError(ctx, "validate storage backend", err)
	}
	config, secrets, err := settings.columns()
	if err != nil {
		return nil, s.internalError(ctx, "create storage backend", err)
	}
	var row db.StorageBackend
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		row, err = queries.CreateStorageBackend(ctx, db.CreateStorageBackendParams{
			PublicID: id.New(id.StorageBackend), Name: name, Type: input.Body.Type,
			Config: config, EncryptedSecrets: s.encrypter.Encrypt(secrets), Enabled: input.Body.Enabled,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, queries, backendEvent(ctx, audit.StorageBackendCreated, row))
	})
	if isNameConflict(err) {
		return nil, huma.Error409Conflict("A storage backend with this name already exists.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create storage backend", err)
	}
	return s.output(ctx, row)
}

func (s *Service) update(ctx context.Context, input *updateBackendInput) (*backendOutput, error) {
	name, err := validateName(input.Body.Name)
	if err != nil {
		return nil, err
	}
	var row db.StorageBackend
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		current, err := queries.LockStorageBackendByPublicID(ctx, input.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		stored, err := s.storedCredentials(current, input.Body.S3)
		if err != nil {
			return err
		}
		settings, err := buildSettings(current.Type, input.Body.Local, input.Body.S3, stored)
		if err != nil {
			return err
		}
		config, secrets, err := settings.columns()
		if err != nil {
			return err
		}
		row, err = queries.UpdateStorageBackend(ctx, db.UpdateStorageBackendParams{
			PublicID: input.ID, Name: name, Config: config,
			EncryptedSecrets: s.encrypter.Encrypt(secrets), Enabled: input.Body.Enabled,
		})
		if err != nil {
			return err
		}
		event := backendEvent(ctx, audit.StorageBackendUpdated, row)
		if current.Name != row.Name {
			event.Details.PreviousName = current.Name
		}
		return audit.Record(ctx, queries, event)
	})
	if isNameConflict(err) {
		return nil, huma.Error409Conflict("A storage backend with this name already exists.")
	}
	if err != nil {
		return nil, s.requestError(ctx, "update storage backend", err)
	}
	return s.output(ctx, row)
}

// storedCredentials returns the stored credentials when the input keeps them.
// It returns nil when the input contains new credentials.
func (s *Service) storedCredentials(row db.StorageBackend, input *S3Input) (*s3Credentials, error) {
	if row.Type != TypeS3 || input == nil || strings.TrimSpace(input.AccessKeyID) != "" || strings.TrimSpace(input.SecretAccessKey) != "" {
		return nil, nil
	}
	backend, err := backendFromRow(row)
	if err != nil {
		return nil, err
	}
	endpoint, err := normalizeEndpoint(strings.TrimSpace(input.Endpoint))
	if err != nil {
		return nil, err
	}
	// Requests contain the access key ID, so stored credentials must not go to a different service.
	if endpoint != backend.S3.Endpoint {
		return nil, invalid("body.s3", "Enter the credentials again when you change the endpoint.")
	}
	credentials, err := s.decryptCredentials(row)
	if errors.Is(err, crypt.ErrDecrypt) {
		return nil, invalid("body.s3", undecryptableCredentials)
	}
	if err != nil {
		return nil, err
	}
	return &credentials, nil
}

const undecryptableCredentials = "The stored credentials cannot be decrypted with the current APP_KEY. Enter the credentials again."

func (s *Service) decryptCredentials(row db.StorageBackend) (s3Credentials, error) {
	plaintext, err := s.encrypter.Decrypt(row.EncryptedSecrets)
	if err != nil {
		return s3Credentials{}, err
	}
	var credentials s3Credentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return s3Credentials{}, fmt.Errorf("decode credentials of storage backend %s: %w", row.PublicID, err)
	}
	return credentials, nil
}

func (s *Service) delete(ctx context.Context, input *backendInput) (*struct{}, error) {
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		row, err := queries.GetStorageBackendByPublicID(ctx, input.ID)
		if err != nil {
			return err
		}
		deleted, err := queries.DeleteStorageBackend(ctx, input.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return pgx.ErrNoRows
		}
		return audit.Record(ctx, queries, backendEvent(ctx, audit.StorageBackendDeleted, row))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if isReferenceConflict(err) {
		return nil, huma.Error409Conflict("The storage backend is in use.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete storage backend", err)
	}
	return &struct{}{}, nil
}

func backendEvent(ctx context.Context, action audit.Action, row db.StorageBackend) audit.Event {
	admin, _ := auth.UserFromContext(ctx)
	enabled := row.Enabled
	return audit.Event{
		Action: action, ActorID: admin.ID, TargetID: row.PublicID,
		Details: audit.Details{Name: row.Name, BackendType: row.Type, BackendEnabled: &enabled},
	}
}

func (s *Service) output(ctx context.Context, row db.StorageBackend) (*backendOutput, error) {
	backend, err := backendFromRow(row)
	if err != nil {
		return nil, s.internalError(ctx, "decode storage backend", err)
	}
	return &backendOutput{Body: backend}, nil
}

// requestError returns client errors unchanged and hides other errors.
func (s *Service) requestError(ctx context.Context, operation string, err error) error {
	if statusErr, ok := errors.AsType[huma.StatusError](err); ok && statusErr.GetStatus() < http.StatusInternalServerError {
		return statusErr
	}
	return s.internalError(ctx, operation, err)
}

// notFound builds a new error for each request, because the request ID transformer changes the error.
func notFound() error {
	return huma.Error404NotFound("The storage backend does not exist.")
}

func isNameConflict(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505" && pgErr.ConstraintName == "storage_backends_name_lower_key"
}

func isReferenceConflict(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503"
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Storage settings are unavailable.")
}
