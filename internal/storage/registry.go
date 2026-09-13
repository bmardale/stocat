package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Service) ObjectStore(ctx context.Context, backendID string) (ObjectStore, error) {
	row, err := s.queries.GetEnabledBackendByPublicID(ctx, backendID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("load enabled storage backend: %w", ErrConfiguration)
	}
	if err != nil {
		return nil, fmt.Errorf("load storage backend: %w: %w", ErrTemporary, err)
	}
	switch row.Type {
	case TypeLocal:
		var config LocalConfig
		if err := json.Unmarshal(row.Config, &config); err != nil {
			return nil, fmt.Errorf("decode local storage backend: %w: %w", ErrConfiguration, err)
		}
		return NewLocalStore(config.Root)
	case TypeS3:
		var config S3Config
		if err := json.Unmarshal(row.Config, &config); err != nil {
			return nil, fmt.Errorf("decode S3 storage backend: %w: %w", ErrConfiguration, err)
		}
		credentials, err := s.decryptCredentials(row)
		if err != nil {
			return nil, fmt.Errorf("decrypt S3 storage backend: %w: %w", ErrConfiguration, err)
		}
		return NewS3Store(config, credentials.AccessKeyID, credentials.SecretAccessKey), nil
	default:
		return nil, fmt.Errorf("unknown storage backend type: %w", ErrConfiguration)
	}
}
