package storage

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/crypt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

const checkTimeout = 15 * time.Second

// checkPayload is the content of the object that a check writes, reads, and deletes.
var checkPayload = []byte("stocat connection check\n")

type ConnectionCheck struct {
	OK      bool   `json:"ok"`
	Message string `json:"message" doc:"Result of the check. It never contains credentials."`
}

type checkOutput struct {
	Body ConnectionCheck
}

type checkSettingsInput struct {
	Body struct {
		ID    string       `json:"id,omitempty" maxLength:"64" doc:"ID of a saved backend. Empty S3 credentials use the stored credentials of this backend, if the endpoint does not change."`
		Type  string       `json:"type" enum:"local,s3"`
		Local *LocalConfig `json:"local,omitempty" doc:"Required for local backends."`
		S3    *S3Input     `json:"s3,omitempty" doc:"Required for S3 backends."`
	}
}

// checkFailure is a failed check. The message goes to the client, so it must not contain credentials.
type checkFailure struct {
	message string
	err     error
}

func (f *checkFailure) Error() string {
	if f.err == nil {
		return f.message
	}
	return f.message + ": " + f.err.Error()
}

func (f *checkFailure) Unwrap() error { return f.err }

func failure(message string, err error) error {
	return &checkFailure{message: message, err: err}
}

func failureMessage(err error) string {
	if f, ok := errors.AsType[*checkFailure](err); ok {
		return f.message
	}
	return "The connection check failed."
}

func (s *Service) registerChecks(group huma.API) {
	const description = "The server writes, reads, and deletes a small object. " +
		"A completed check returns status 200. The ok field contains the result."
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-check-settings", Method: http.MethodPost, Path: "/check",
		Summary: "Check the connection with unsaved settings", Description: description, MaxBodyBytes: 16384,
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.checkSettings)
	huma.Register(group, huma.Operation{
		OperationID: "storage-backends-check", Method: http.MethodPost, Path: "/{id}/check",
		Summary: "Check the connection of a saved backend", Description: description,
		Errors: []int{http.StatusNotFound},
	}, s.checkSaved)
}

func (s *Service) checkSettings(ctx context.Context, input *checkSettingsInput) (*checkOutput, error) {
	var stored *s3Credentials
	if input.Body.ID != "" {
		row, err := s.queries.GetStorageBackendByPublicID(ctx, input.Body.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound()
		}
		if err != nil {
			return nil, s.internalError(ctx, "get storage backend", err)
		}
		if row.Type != input.Body.Type {
			return nil, invalid("body.type", "The type is different from the type of the saved backend.")
		}
		if stored, err = s.storedCredentials(row, input.Body.S3); err != nil {
			return nil, s.requestError(ctx, "check storage backend", err)
		}
	}
	settings, err := buildSettings(input.Body.Type, input.Body.Local, input.Body.S3, stored)
	if err != nil {
		return nil, s.requestError(ctx, "check storage backend", err)
	}
	return s.check(ctx, settings), nil
}

func (s *Service) checkSaved(ctx context.Context, input *backendInput) (*checkOutput, error) {
	row, err := s.queries.GetStorageBackendByPublicID(ctx, input.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "get storage backend", err)
	}
	settings, err := s.storedSettings(row)
	if errors.Is(err, crypt.ErrDecrypt) {
		return &checkOutput{Body: ConnectionCheck{Message: undecryptableCredentials}}, nil
	}
	if err != nil {
		return nil, s.internalError(ctx, "check storage backend", err)
	}
	return s.check(ctx, settings), nil
}

func (s *Service) storedSettings(row db.StorageBackend) (settings, error) {
	backend, err := backendFromRow(row)
	if err != nil {
		return settings{}, err
	}
	result := settings{local: backend.Local, s3: backend.S3}
	if result.s3 != nil {
		if result.credentials, err = s.decryptCredentials(row); err != nil {
			return settings{}, err
		}
	}
	return result, nil
}

func (s *Service) check(ctx context.Context, settings settings) *checkOutput {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var err error
	if settings.local != nil {
		err = checkLocal(settings.local.Root)
	} else {
		err = checkS3(ctx, *settings.s3, settings.credentials)
	}
	if err != nil {
		s.log.WarnContext(ctx, "storage backend check failed", "error", err)
		return &checkOutput{Body: ConnectionCheck{Message: failureMessage(err)}}
	}
	return &checkOutput{Body: ConnectionCheck{OK: true, Message: "The server can write, read, and delete objects."}}
}
