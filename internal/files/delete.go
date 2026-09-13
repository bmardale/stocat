package files

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type deleteObjectsArgs struct {
	BackendID string   `json:"backend_id"`
	Keys      []string `json:"keys"`
}

func (deleteObjectsArgs) Kind() string { return "delete-objects" }

func (deleteObjectsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "maintenance", MaxAttempts: 25}
}

type deleteObjectsWorker struct {
	river.WorkerDefaults[deleteObjectsArgs]
	stores *storage.Service
}

func (w *deleteObjectsWorker) Work(ctx context.Context, job *river.Job[deleteObjectsArgs]) error {
	store, err := w.stores.ObjectStore(ctx, job.Args.BackendID)
	if err != nil {
		return fmt.Errorf("open backend %s: %w", job.Args.BackendID, err)
	}
	defer func() { _ = store.Close() }()
	for _, key := range job.Args.Keys {
		if err := store.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrMissing) {
			return fmt.Errorf("delete object %s: %w", key, err)
		}
	}
	return nil
}

func (s *Service) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &deleteObjectsWorker{stores: s.stores})
}

func (s *Service) UseQueue(queue *river.Client[pgx.Tx]) { s.queue = queue }

// remove deletes the file rows in one transaction. A River job deletes the stored objects after the commit.
func (s *Service) remove(ctx context.Context, input *fileInput) (*struct{}, error) {
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("File deletion is temporarily unavailable.")
	}
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Publication locks its upload session before the library. Keep the same order to prevent a deadlock.
		if err := q.DetachUploadSessionsFromNode(ctx, pgtype.Int8{Int64: file.ID, Valid: true}); err != nil {
			return fmt.Errorf("detach upload sessions: %w", err)
		}
		// The library lock stops publication from reusing a blob while this transaction deletes it.
		if err := q.LockLibraryByID(ctx, file.LibraryID); err != nil {
			return fmt.Errorf("lock library: %w", err)
		}
		// PostgreSQL checks a RESTRICT foreign key at once, also when the constraint is deferrable.
		if err := q.ClearFileCurrentVersion(ctx, file.ID); err != nil {
			return fmt.Errorf("clear current version: %w", err)
		}
		blobIDs, err := q.DeleteFileVersions(ctx, file.ID)
		if err != nil {
			return fmt.Errorf("delete file versions: %w", err)
		}
		deleted, err := q.DeleteFileNode(ctx, file.ID)
		if err != nil {
			return fmt.Errorf("delete file node: %w", err)
		}
		if deleted == 0 {
			return pgx.ErrNoRows
		}
		locations, err := q.DeleteUnreferencedBlobLocations(ctx, blobIDs)
		if err != nil {
			return fmt.Errorf("delete blob locations: %w", err)
		}
		if err := q.DeleteUnreferencedBlobs(ctx, blobIDs); err != nil {
			return fmt.Errorf("delete blobs: %w", err)
		}
		return s.enqueueObjectDeletion(ctx, tx, locations)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fileNotFound()
	}
	if isUploadReference(err) {
		return nil, huma.Error409Conflict("An upload changed the file during deletion. Try again.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete file", err)
	}
	return nil, nil
}

// A new upload session can refer to the file after the transaction detached the sessions.
func isUploadReference(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503" && strings.HasPrefix(pgErr.ConstraintName, "upload_sessions_")
}

func (s *Service) enqueueObjectDeletion(ctx context.Context, tx pgx.Tx, locations []db.DeleteUnreferencedBlobLocationsRow) error {
	keys := make(map[string][]string)
	for _, location := range locations {
		keys[location.BackendPublicID] = append(keys[location.BackendPublicID], location.ObjectKey)
	}
	for backendID, objectKeys := range keys {
		if _, err := s.queue.InsertTx(ctx, tx, deleteObjectsArgs{BackendID: backendID, Keys: objectKeys}, nil); err != nil {
			return fmt.Errorf("enqueue object deletion: %w", err)
		}
	}
	return nil
}
