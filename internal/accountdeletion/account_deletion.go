// Package accountdeletion removes user data and schedules object cleanup.
package accountdeletion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const objectBatchSize = 100

var ErrQueueUnavailable = errors.New("user deletion queue is unavailable")

type UserCheck func(context.Context, *db.Queries, db.User) error

type ObjectTarget struct {
	BackendID string `json:"backend_id"`
	Key       string `json:"key"`
}

type ObjectCleaner func(context.Context, []ObjectTarget) error

type Service struct {
	pool           *pgxpool.Pool
	queries        *db.Queries
	log            *slog.Logger
	queue          *river.Client[pgx.Tx]
	stagingCleaner func([]string)
	objectCleaner  ObjectCleaner
}

type deleteObjectsArgs struct {
	Objects []ObjectTarget `json:"objects"`
}

func (deleteObjectsArgs) Kind() string { return "account-delete-objects" }

func (deleteObjectsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "maintenance", MaxAttempts: 25}
}

type deleteObjectsWorker struct {
	river.WorkerDefaults[deleteObjectsArgs]
	cleaner ObjectCleaner
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), log: logger}
}

func (s *Service) UseQueue(queue *river.Client[pgx.Tx]) { s.queue = queue }

func (s *Service) UseStagingCleaner(cleaner func([]string)) { s.stagingCleaner = cleaner }

func (s *Service) UseObjectCleaner(cleaner ObjectCleaner) { s.objectCleaner = cleaner }

func (s *Service) DeleteUser(ctx context.Context, userID, actorID int64) error {
	return s.DeleteUserWithCheck(ctx, userID, actorID, nil)
}

func (s *Service) DeleteUserWithCheck(ctx context.Context, userID, actorID int64, check UserCheck) error {
	var stagingKeys []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		user, err := queries.GetUserByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(ctx, queries, user); err != nil {
				return err
			}
		}
		if _, err := queries.LockUserUploadSessions(ctx, user.ID); err != nil {
			return fmt.Errorf("lock user upload sessions: %w", err)
		}
		if _, err := queries.LockUserLibraries(ctx, user.ID); err != nil {
			return fmt.Errorf("lock user libraries: %w", err)
		}

		objects, uploads, err := s.deletionTargets(ctx, queries, user.ID)
		if err != nil {
			return err
		}
		stagingKeys = make([]string, 0, len(uploads))
		for _, upload := range uploads {
			stagingKeys = append(stagingKeys, upload.StagingKey)
		}
		if len(objects) > 0 && s.queue == nil {
			return ErrQueueUnavailable
		}

		if err := queries.DeleteUserUploadSessions(ctx, user.ID); err != nil {
			return fmt.Errorf("delete user upload sessions: %w", err)
		}
		if err := queries.ClearUserCurrentVersions(ctx, user.ID); err != nil {
			return fmt.Errorf("clear user file versions: %w", err)
		}
		if err := queries.DeleteUserFileVersions(ctx, user.ID); err != nil {
			return fmt.Errorf("delete user file versions: %w", err)
		}
		if err := queries.DeleteUserBlobLocations(ctx, user.ID); err != nil {
			return fmt.Errorf("delete user blob locations: %w", err)
		}
		if err := queries.DeleteUserBlobs(ctx, user.ID); err != nil {
			return fmt.Errorf("delete user blobs: %w", err)
		}
		for {
			leaves, err := queries.DeleteUserLeafNodes(ctx, user.ID)
			if err != nil {
				return fmt.Errorf("delete user nodes: %w", err)
			}
			if len(leaves) == 0 {
				break
			}
		}
		if _, err := queries.DeleteUserLibraries(ctx, user.ID); err != nil {
			return fmt.Errorf("delete user libraries: %w", err)
		}
		if err := audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountDeleted, ActorID: actorID, SubjectID: user.ID, TargetID: user.PublicID,
			Details: audit.Details{Name: user.Name, Email: user.Email},
		}); err != nil {
			return err
		}
		if err := s.enqueueObjectDeletion(ctx, tx, objects); err != nil {
			return err
		}
		deleted, err := queries.DeleteUser(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		if deleted == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.stagingCleaner != nil && len(stagingKeys) > 0 {
		s.stagingCleaner(stagingKeys)
	}
	return nil
}

type uploadTarget struct {
	BackendID      string
	DestinationKey string
	StagingKey     string
}

func (s *Service) deletionTargets(
	ctx context.Context, queries *db.Queries, userID int64,
) ([]ObjectTarget, []uploadTarget, error) {
	blobs, err := queries.ListUserBlobDeletionTargets(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list user blob objects: %w", err)
	}
	uploads, err := queries.ListUserUploadDeletionTargets(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list user upload objects: %w", err)
	}
	objects := make([]ObjectTarget, 0, len(blobs)+len(uploads))
	seen := make(map[string]struct{}, cap(objects))
	addObject := func(backendID, key string) {
		if key == "" {
			return
		}
		unique := backendID + "\x00" + key
		if _, ok := seen[unique]; ok {
			return
		}
		seen[unique] = struct{}{}
		objects = append(objects, ObjectTarget{BackendID: backendID, Key: key})
	}
	for _, blob := range blobs {
		addObject(blob.BackendPublicID, blob.ObjectKey)
	}
	cleanup := make([]uploadTarget, 0, len(uploads))
	for _, upload := range uploads {
		addObject(upload.BackendPublicID, upload.DestinationKey)
		cleanup = append(cleanup, uploadTarget{
			BackendID: upload.BackendPublicID, DestinationKey: upload.DestinationKey, StagingKey: upload.StagingKey,
		})
	}
	return objects, cleanup, nil
}

func (s *Service) enqueueObjectDeletion(ctx context.Context, tx pgx.Tx, objects []ObjectTarget) error {
	if len(objects) == 0 {
		return nil
	}
	for start := 0; start < len(objects); start += objectBatchSize {
		end := min(start+objectBatchSize, len(objects))
		if _, err := s.queue.InsertTx(ctx, tx, deleteObjectsArgs{Objects: objects[start:end]}, nil); err != nil {
			return fmt.Errorf("enqueue user object deletion: %w", err)
		}
	}
	return nil
}

func (s *Service) Workers() func(*river.Workers) []*river.PeriodicJob {
	return func(workers *river.Workers) []*river.PeriodicJob {
		river.AddWorker(workers, &deleteObjectsWorker{cleaner: s.objectCleaner})
		return nil
	}
}

func (w *deleteObjectsWorker) Work(ctx context.Context, job *river.Job[deleteObjectsArgs]) error {
	if w.cleaner == nil {
		return ErrQueueUnavailable
	}
	return w.cleaner(ctx, job.Args.Objects)
}
