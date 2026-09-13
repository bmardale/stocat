package trash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

var errPurgeBlocked = errors.New("an active upload blocks the permanent deletion")

type purgeArgs struct{}

func (purgeArgs) Kind() string { return "purge-trash" }

type purgeWorker struct {
	river.WorkerDefaults[purgeArgs]
	service *Service
}

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[purgeArgs]) error {
	roots, err := w.service.queries.ListExpiredTrashRoots(ctx, db.ListExpiredTrashRootsParams{
		Cutoff:    pgtype.Timestamptz{Time: time.Now().Add(-Retention), Valid: true},
		PageLimit: pageSize,
	})
	if err != nil {
		return fmt.Errorf("list expired trash: %w", err)
	}
	for _, root := range roots {
		node, err := w.service.queries.GetNodeByID(ctx, root.ID)
		if err != nil {
			w.service.log.ErrorContext(ctx, "load expired trashed node", "error", err, "node", root.PublicID)
			continue
		}
		if err := w.service.purgeNode(ctx, node); err != nil {
			w.service.log.WarnContext(ctx, "purge expired trashed node", "error", err, "node", root.PublicID)
		}
	}
	return nil
}

func (s *Service) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &purgeWorker{service: s})
}

func (s *Service) UseQueue(queue *river.Client[pgx.Tx]) { s.queue = queue }

func (s *Service) PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return purgeArgs{}, &river.InsertOpts{Queue: "maintenance", MaxAttempts: 8}
		}, &river.PeriodicJobOpts{ID: "purge-trash", RunOnStart: true}),
	}
}

// purgeNode deletes a trashed node and its subtree in one transaction.
// A River job deletes the stored objects after the commit.
func (s *Service) purgeNode(ctx context.Context, node db.Node) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		blocked, err := q.CountActiveUploadSessionsInSubtree(ctx, node.ID)
		if err != nil {
			return fmt.Errorf("count active upload sessions: %w", err)
		}
		if blocked > 0 {
			return errPurgeBlocked
		}
		// The library lock stops publication from reusing a blob while this transaction deletes it.
		if err := q.LockLibraryByID(ctx, node.LibraryID); err != nil {
			return fmt.Errorf("lock library: %w", err)
		}
		rootID, err := q.GetLibraryRootNodeID(ctx, node.LibraryID)
		if err != nil {
			return fmt.Errorf("get library root: %w", err)
		}
		if err := q.PrepareUploadSessionsForSubtreeRemoval(ctx, db.PrepareUploadSessionsForSubtreeRemovalParams{
			RootNodeID: rootID, NodeID: node.ID,
		}); err != nil {
			return fmt.Errorf("detach upload sessions: %w", err)
		}
		if err := q.ClearSubtreeCurrentVersion(ctx, node.ID); err != nil {
			return fmt.Errorf("clear current version: %w", err)
		}
		blobIDs, err := q.DeleteSubtreeFileVersions(ctx, node.ID)
		if err != nil {
			return fmt.Errorf("delete file versions: %w", err)
		}
		deleted, err := q.DeleteSubtreeNodes(ctx, node.ID)
		if err != nil {
			return fmt.Errorf("delete nodes: %w", err)
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
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errPurgeBlocked) {
		return err
	}
	if err != nil {
		return s.internalError(ctx, "purge trashed node", err)
	}
	return nil
}

type deleteObjectsArgs struct {
	BackendID string   `json:"backend_id"`
	Keys      []string `json:"keys"`
}

func (deleteObjectsArgs) Kind() string { return "delete-objects" }

func (deleteObjectsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "maintenance", MaxAttempts: 25}
}

// The files service registers the delete-objects worker. This service only enqueues the jobs.
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

var (
	_ river.JobArgs               = purgeArgs{}
	_ river.JobArgsWithInsertOpts = deleteObjectsArgs{}
	_ river.Worker[purgeArgs]     = (*purgeWorker)(nil)
)
