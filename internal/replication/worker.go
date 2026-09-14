package replication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type SyncArgs struct {
	ReplicationID int64 `json:"replication_id" river:"unique"`
}

func (SyncArgs) Kind() string { return "sync-library-replication" }

func (SyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "replication", MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type syncWorker struct {
	river.WorkerDefaults[SyncArgs]
	service *Service
}

func (w *syncWorker) Timeout(*river.Job[SyncArgs]) time.Duration { return -1 }

func (w *syncWorker) Work(ctx context.Context, job *river.Job[SyncArgs]) error {
	err := w.service.reconcile(ctx, job.Args.ReplicationID)
	if err != nil && job.Attempt >= job.MaxAttempts {
		message := "The last sync failed. Use Sync now to try again."
		if stateErr := w.service.queries.MarkReplicationFailed(ctx, db.MarkReplicationFailedParams{ID: job.Args.ReplicationID, LastError: pgtype.Text{String: message, Valid: true}}); stateErr != nil {
			return errors.Join(err, stateErr)
		}
	}
	return err
}

type scheduleArgs struct{}

func (scheduleArgs) Kind() string { return "schedule-library-replications" }

type scheduleWorker struct {
	river.WorkerDefaults[scheduleArgs]
	service *Service
}

func (w *scheduleWorker) Work(ctx context.Context, _ *river.Job[scheduleArgs]) error {
	ids, err := w.service.queries.ListReplicationIDs(ctx)
	if err != nil {
		return fmt.Errorf("list replications: %w", err)
	}
	for _, replicationID := range ids {
		if err := w.service.enqueue(ctx, replicationID); err != nil {
			return fmt.Errorf("enqueue replication %d: %w", replicationID, err)
		}
	}
	return nil
}

func (s *Service) AddWorkers(workers *river.Workers) []*river.PeriodicJob {
	river.AddWorker(workers, &syncWorker{service: s})
	river.AddWorker(workers, &scheduleWorker{service: s})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return scheduleArgs{}, &river.InsertOpts{Queue: "maintenance", MaxAttempts: 8}
		}, &river.PeriodicJobOpts{ID: "schedule-library-replications", RunOnStart: true}),
	}
}

func (s *Service) reconcile(ctx context.Context, replicationID int64) error {
	replication, err := s.queries.GetReplicationByID(ctx, replicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load replication: %w", err)
	}
	if err := s.queries.MarkReplicationSyncing(ctx, replicationID); err != nil {
		return fmt.Errorf("mark replication syncing: %w", err)
	}
	rows, err := s.queries.ListReplicationSourceNodes(ctx, db.ListReplicationSourceNodesParams{
		ReplicationID: replicationID, SourceLibraryID: replication.SourceLibraryID,
	})
	if err != nil {
		return fmt.Errorf("list source nodes: %w", err)
	}
	nodeIDs := map[int64]int64{replication.SourceRootNodeID: replication.DestinationRootNodeID}
	for _, row := range rows {
		parentID, ok := nodeIDs[row.ParentID.Int64]
		if !ok {
			return fmt.Errorf("map parent of node %d", row.ID)
		}
		destinationNodeID := row.DestinationNodeID.Int64
		if !row.DestinationNodeID.Valid {
			destinationNodeID, err = s.createMappedNode(ctx, replication, row, parentID)
			if err != nil {
				return err
			}
		} else if err := s.queries.UpdateReplicatedNode(ctx, db.UpdateReplicatedNodeParams{
			ID: destinationNodeID, ParentID: pgtype.Int8{Int64: parentID, Valid: true}, Name: row.Name,
			EncryptedName: row.EncryptedName, NameToken: row.NameToken, Revision: row.Revision, TrashedAt: row.TrashedAt,
		}); err != nil {
			return fmt.Errorf("update destination node: %w", err)
		}
		nodeIDs[row.ID] = destinationNodeID
		if row.Kind == "file" && !bytes.Equal(row.DedupFingerprint, row.DestinationDedupFingerprint) {
			if err := s.replicateFile(ctx, replication, row, destinationNodeID); err != nil {
				return err
			}
		}
	}
	removed, err := s.queries.ListRemovedReplicationNodes(ctx, db.ListRemovedReplicationNodesParams{
		ReplicationID: replicationID, LibraryID: replication.SourceLibraryID,
	})
	if err != nil {
		return fmt.Errorf("list removed source nodes: %w", err)
	}
	for _, destinationNodeID := range removed {
		if err := s.queries.TrashReplicatedNode(ctx, destinationNodeID); err != nil {
			return fmt.Errorf("trash removed destination node: %w", err)
		}
		if err := s.queries.DeleteReplicationNodeMapping(ctx, db.DeleteReplicationNodeMappingParams{
			ReplicationID: replicationID, DestinationNodeID: destinationNodeID,
		}); err != nil {
			return fmt.Errorf("delete node mapping: %w", err)
		}
	}
	if err := s.queries.MarkReplicationReady(ctx, replicationID); err != nil {
		return fmt.Errorf("mark replication ready: %w", err)
	}
	return nil
}

func (s *Service) createMappedNode(ctx context.Context, replication db.GetReplicationByIDRow, row db.ListReplicationSourceNodesRow, parentID int64) (int64, error) {
	var destinationNodeID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		node, err := queries.CreateReplicatedNode(ctx, db.CreateReplicatedNodeParams{
			PublicID: id.New(id.Node), LibraryID: replication.DestinationLibraryID,
			ParentID: pgtype.Int8{Int64: parentID, Valid: true}, Kind: row.Kind, Name: row.Name,
			EncryptedName: row.EncryptedName, NameToken: row.NameToken, Revision: row.Revision, TrashedAt: row.TrashedAt,
		})
		if err != nil {
			return err
		}
		destinationNodeID = node.ID
		return queries.MapReplicationNode(ctx, db.MapReplicationNodeParams{
			ReplicationID: replication.ID, SourceNodeID: row.ID, DestinationNodeID: destinationNodeID,
		})
	})
	if err != nil {
		return 0, fmt.Errorf("create destination node: %w", err)
	}
	return destinationNodeID, nil
}

func (s *Service) replicateFile(ctx context.Context, replication db.GetReplicationByIDRow, row db.ListReplicationSourceNodesRow, destinationNodeID int64) error {
	blob, err := s.queries.GetDestinationBlobByFingerprint(ctx, db.GetDestinationBlobByFingerprintParams{
		LibraryID: replication.DestinationLibraryID, DedupFingerprint: row.DedupFingerprint,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		blob, err = s.copyBlob(ctx, replication, row)
	}
	if err != nil {
		return err
	}
	ordinal, err := s.queries.NextFileVersionOrdinal(ctx, destinationNodeID)
	if err != nil {
		return fmt.Errorf("get destination version: %w", err)
	}
	version, err := s.queries.CreateReplicatedFileVersion(ctx, db.CreateReplicatedFileVersionParams{
		PublicID: id.New(id.FileVersion), NodeID: destinationNodeID, LibraryID: replication.DestinationLibraryID,
		Ordinal: int64(ordinal), BlobID: blob.ID, SizeBytes: row.SizeBytes.Int64,
	})
	if err != nil {
		return fmt.Errorf("create destination version: %w", err)
	}
	if err := s.queries.SetReplicatedNodeVersion(ctx, db.SetReplicatedNodeVersionParams{ID: destinationNodeID, CurrentVersionID: pgtype.Int8{Int64: version.ID, Valid: true}}); err != nil {
		return fmt.Errorf("select destination version: %w", err)
	}
	return nil
}

func (s *Service) copyBlob(ctx context.Context, replication db.GetReplicationByIDRow, row db.ListReplicationSourceNodesRow) (db.Blob, error) {
	source, err := s.stores.ObjectStore(ctx, replication.SourceBackendPublicID)
	if err != nil {
		return db.Blob{}, fmt.Errorf("open source backend: %w", err)
	}
	defer func() { _ = source.Close() }()
	destination, err := s.stores.ObjectStore(ctx, replication.DestinationBackendPublicID)
	if err != nil {
		return db.Blob{}, fmt.Errorf("open destination backend: %w", err)
	}
	defer func() { _ = destination.Close() }()
	object, err := source.Open(ctx, row.ObjectKey.String, nil)
	if err != nil {
		return db.Blob{}, fmt.Errorf("open source object: %w", err)
	}
	defer func() { _ = object.Body.Close() }()
	objectKey := "replicas/" + replication.PublicID + "/" + row.BlobPublicID.String
	stored, err := destination.Put(ctx, objectKey, object.Body, row.StoredSizeBytes.Int64)
	if errors.Is(err, storage.ErrExists) {
		stored, err = destination.Stat(ctx, objectKey)
	}
	if err != nil {
		return db.Blob{}, fmt.Errorf("write destination object: %w", err)
	}
	if stored.Size != row.StoredSizeBytes.Int64 {
		return db.Blob{}, fmt.Errorf("destination object size does not match")
	}
	var blob db.Blob
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		var createErr error
		blob, createErr = queries.CreateReplicatedBlob(ctx, db.CreateReplicatedBlobParams{
			PublicID: id.New(id.Blob), LibraryID: replication.DestinationLibraryID,
			SizeBytes: row.StoredSizeBytes.Int64, CiphertextSha256: row.CiphertextSha256,
			DedupFingerprint: row.DedupFingerprint, EncryptionFormat: row.EncryptionFormat,
			EncryptedFileKey: row.EncryptedFileKey,
		})
		if createErr != nil {
			return createErr
		}
		_, createErr = queries.CreateReplicatedBlobLocation(ctx, db.CreateReplicatedBlobLocationParams{
			PublicID: id.New(id.BlobLocation), BlobID: blob.ID, LibraryID: replication.DestinationLibraryID,
			BackendID: replication.DestinationBackendID, ObjectKey: objectKey,
		})
		return createErr
	})
	if err != nil {
		return db.Blob{}, fmt.Errorf("save destination blob: %w", err)
	}
	return blob, nil
}
