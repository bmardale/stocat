package files

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/replication"
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

type purgeTrashArgs struct{}

func (purgeTrashArgs) Kind() string { return "purge-trash" }

type purgeTrashWorker struct {
	river.WorkerDefaults[purgeTrashArgs]
	service *Service
}

func (w *purgeTrashWorker) Work(ctx context.Context, _ *river.Job[purgeTrashArgs]) error {
	for {
		ids, err := w.service.queries.ListExpiredTrashedFileIDs(ctx, 100)
		if err != nil {
			return fmt.Errorf("list expired trash: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, fileID := range ids {
			if err := w.service.permanentlyDelete(ctx, fileID, true); err != nil {
				return fmt.Errorf("purge file %d: %w", fileID, err)
			}
		}
	}
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

func (s *Service) AddWorkers(workers *river.Workers) []*river.PeriodicJob {
	river.AddWorker(workers, &deleteObjectsWorker{stores: s.stores})
	river.AddWorker(workers, &purgeTrashWorker{service: s})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return purgeTrashArgs{}, &river.InsertOpts{Queue: "maintenance", MaxAttempts: 8}
		}, &river.PeriodicJobOpts{ID: "purge-trash", RunOnStart: true}),
	}
}

func (s *Service) UseQueue(queue *river.Client[pgx.Tx]) { s.queue = queue }

// remove moves an active file to the trash.
func (s *Service) remove(ctx context.Context, input *fileInput) (*struct{}, error) {
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFromContext(ctx)
	if err := replication.EnsureLibraryWritable(ctx, s.queries, file.LibraryPublicID, user.ID); err != nil {
		return nil, err
	}
	if _, err := s.queries.TrashFileNode(ctx, file.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fileNotFound()
		}
		return nil, s.internalError(ctx, "trash file", err)
	}
	return nil, nil
}

func (s *Service) permanentlyDelete(ctx context.Context, fileID int64, expiredOnly bool) error {
	if s.queue == nil {
		return errors.New("file deletion queue is unavailable")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		var libraryID int64
		if expiredOnly {
			row, err := q.GetExpiredTrashedFileForUpdate(ctx, fileID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("lock expired file: %w", err)
			}
			libraryID = row.LibraryID
		} else {
			row, err := q.GetTrashedFileForUpdate(ctx, fileID)
			if err != nil {
				return fmt.Errorf("lock trashed file: %w", err)
			}
			libraryID = row.LibraryID
		}
		// Publication locks its upload session before the library. Keep the same order to prevent a deadlock.
		if err := q.DetachUploadSessionsFromNode(ctx, pgtype.Int8{Int64: fileID, Valid: true}); err != nil {
			return fmt.Errorf("detach upload sessions: %w", err)
		}
		// The library lock stops publication from reusing a blob while this transaction deletes it.
		if err := q.LockLibraryByID(ctx, libraryID); err != nil {
			return fmt.Errorf("lock library: %w", err)
		}
		// PostgreSQL checks a RESTRICT foreign key at once, also when the constraint is deferrable.
		if err := q.ClearFileCurrentVersion(ctx, fileID); err != nil {
			return fmt.Errorf("clear current version: %w", err)
		}
		blobIDs, err := q.DeleteFileVersions(ctx, fileID)
		if err != nil {
			return fmt.Errorf("delete file versions: %w", err)
		}
		deleted, err := q.DeleteFileNode(ctx, fileID)
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
}

func (s *Service) deletePermanently(ctx context.Context, input *fileInput) (*struct{}, error) {
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("File deletion is temporarily unavailable.")
	}
	user, _ := auth.UserFromContext(ctx)
	file, err := s.queries.GetTrashedFileNodeByPublicIDAndOwner(ctx, db.GetTrashedFileNodeByPublicIDAndOwnerParams{
		NodePublicID: input.ID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fileNotFound()
	}
	if err != nil {
		return nil, s.internalError(ctx, "load trashed file", err)
	}
	if err := replication.EnsureLibraryWritable(ctx, s.queries, file.LibraryPublicID, user.ID); err != nil {
		return nil, err
	}
	err = s.permanentlyDelete(ctx, file.ID, false)
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
