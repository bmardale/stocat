package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmardale/stocat/internal/storage"
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

// remove moves the file to the trash. The purge worker deletes the stored objects later.
func (s *Service) remove(ctx context.Context, input *fileInput) (*struct{}, error) {
	file, err := s.loadNode(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.queries.TrashNodeSubtree(ctx, file.ID); err != nil {
		return nil, s.internalError(ctx, "trash file", err)
	}
	return nil, nil
}
