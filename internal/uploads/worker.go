package uploads

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type FinalizeArgs struct {
	UploadID int64 `json:"upload_id" river:"unique"`
}

func (FinalizeArgs) Kind() string { return jobFinalize }

func (FinalizeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 8, Queue: "publication", UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type finalizeWorker struct {
	river.WorkerDefaults[FinalizeArgs]
	service *Service
	stores  *storage.Service
}

type expireArgs struct{}

func (expireArgs) Kind() string { return "expire-uploads" }

type expireWorker struct {
	river.WorkerDefaults[expireArgs]
	service *Service
}

func (w *expireWorker) Work(ctx context.Context, _ *river.Job[expireArgs]) error {
	keys, err := w.service.queries.ExpireUploadSessions(ctx)
	if err != nil {
		return fmt.Errorf("expire upload sessions: %w", err)
	}
	for _, key := range keys {
		if err := w.service.staging.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
			w.service.log.WarnContext(ctx, "remove expired upload", "staging_key", key, "error", err)
		}
	}
	return nil
}

func (w *finalizeWorker) Timeout(*river.Job[FinalizeArgs]) time.Duration { return -1 }

func (w *finalizeWorker) Work(ctx context.Context, job *river.Job[FinalizeArgs]) error {
	err := w.service.finalize(ctx, w.stores, job.Args.UploadID)
	if err != nil && job.Attempt >= job.MaxAttempts {
		stateErr := w.service.finishFailedUpload(ctx, job.Args.UploadID, "publication_failed", "The upload could not be published.")
		return errors.Join(err, stateErr)
	}
	return err
}

// ConfigureQueue creates the River client. Each register function adds the workers of another service.
func (s *Service) ConfigureQueue(stores *storage.Service, register ...func(*river.Workers) []*river.PeriodicJob) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &finalizeWorker{service: s, stores: stores})
	river.AddWorker(workers, &expireWorker{service: s})
	periodicJobs := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return expireArgs{}, &river.InsertOpts{Queue: "maintenance", MaxAttempts: 8}
		}, &river.PeriodicJobOpts{ID: "expire-uploads", RunOnStart: true}),
	}
	for _, add := range register {
		periodicJobs = append(periodicJobs, add(workers)...)
	}
	client, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{
		Logger: s.log, Workers: workers, JobTimeout: -1,
		SoftStopTimeout: 30 * time.Second,
		Queues: map[string]river.QueueConfig{
			"publication": {MaxWorkers: 2},
			"replication": {MaxWorkers: 2},
			"maintenance": {MaxWorkers: 1},
		},
		PeriodicJobs: periodicJobs,
	})
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}
	s.queue = client
	return client, nil
}

func (s *Service) finalize(ctx context.Context, stores *storage.Service, uploadID int64) error {
	upload, err := s.queries.GetUploadPublication(ctx, uploadID)
	if err != nil {
		return fmt.Errorf("load upload publication: %w", err)
	}
	if upload.State == stateCompleted || upload.State == "conflict" || upload.State == "cancelled" {
		return nil
	}
	if upload.State != stateFinalizing {
		return fmt.Errorf("upload %s is in state %s", upload.PublicID, upload.State)
	}
	checksum, err := s.hashStaging(upload.StagingKey)
	if err != nil {
		return err
	}
	contentSize := upload.DeclaredSize
	if upload.EncryptionMode == EncryptionE2EE {
		contentSize, err = s.validateStagingFraming(upload.StagingKey, upload.DeclaredSize)
		if err != nil {
			return s.finishFailedUpload(ctx, upload.ID, "invalid_encryption", "The encrypted file framing is invalid.")
		}
	}
	fingerprint := upload.DedupFingerprint
	if upload.EncryptionMode == EncryptionNone {
		fingerprint = checksum[:]
		if err := s.queries.SetUploadFingerprint(ctx, db.SetUploadFingerprintParams{ID: upload.ID, DedupFingerprint: fingerprint}); err != nil {
			return fmt.Errorf("save upload fingerprint: %w", err)
		}
	}
	blob, err := s.queries.FindBlobByDedupFingerprint(ctx, db.FindBlobByDedupFingerprintParams{
		LibraryID: upload.LibraryID, DedupFingerprint: fingerprint,
	})
	newObject := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !newObject {
		return fmt.Errorf("find upload blob: %w", err)
	}
	store, err := stores.ObjectStore(ctx, upload.BackendPublicID)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if newObject {
		if err := s.putStaging(ctx, store, upload, checksum); err != nil {
			return err
		}
	} else if blob.SizeBytes != upload.DeclaredSize {
		return s.finishUpload(ctx, upload.ID, "failed", "fingerprint_conflict", "The file fingerprint matches content with another size.")
	}
	_, _, reused, err := s.publish(ctx, upload, fingerprint, checksum, contentSize, newObject)
	if errors.Is(err, errPublishConflict) {
		if newObject {
			_ = store.Delete(ctx, upload.DestinationKey)
		}
		return s.finishUpload(ctx, upload.ID, "conflict", "conflict", "The destination changed while the upload was active.")
	}
	if err != nil {
		return err
	}
	if reused || !newObject {
		if err := store.Delete(ctx, upload.DestinationKey); err != nil {
			s.log.WarnContext(ctx, "delete deduplicated upload object", "upload_id", upload.PublicID, "error", err)
		}
	}
	if err := s.staging.Remove(upload.StagingKey); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.WarnContext(ctx, "remove upload staging file", "upload_id", upload.PublicID, "error", err)
	}
	return nil
}

func (s *Service) validateStagingFraming(key string, ciphertextSize int64) (int64, error) {
	file, err := s.staging.Open(key)
	if err != nil {
		return 0, fmt.Errorf("open staged upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	return validateFraming(file, ciphertextSize)
}

func (s *Service) hashStaging(key string) ([sha256.Size]byte, error) {
	file, err := s.staging.Open(key)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open staged upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("hash staged upload: %w", err)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func (s *Service) putStaging(ctx context.Context, store storage.ObjectStore, upload db.GetUploadPublicationRow, checksum [sha256.Size]byte) error {
	file, err := s.staging.Open(upload.StagingKey)
	if err != nil {
		return fmt.Errorf("open staged upload: %w", err)
	}
	object, putErr := store.Put(ctx, upload.DestinationKey, file, upload.DeclaredSize)
	closeErr := file.Close()
	if errors.Is(putErr, storage.ErrExists) {
		return s.verifyStoredObject(ctx, store, upload.DestinationKey, upload.DeclaredSize, checksum)
	}
	if err := errors.Join(putErr, closeErr); err != nil {
		return fmt.Errorf("store upload object: %w", err)
	}
	if object.Size != upload.DeclaredSize || object.SHA256 != checksum {
		return fmt.Errorf("stored upload verification failed")
	}
	return nil
}

func (s *Service) verifyStoredObject(ctx context.Context, store storage.ObjectStore, key string, size int64, checksum [sha256.Size]byte) error {
	object, err := store.Open(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("open existing upload object: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, object.Body)
	closeErr := object.Body.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("verify existing upload object: %w", err)
	}
	if written != size || !equalHash(hash.Sum(nil), checksum[:]) {
		return fmt.Errorf("existing upload object has different content")
	}
	return nil
}

func (s *Service) publish(ctx context.Context, upload db.GetUploadPublicationRow, fingerprint []byte, checksum [sha256.Size]byte, contentSize int64, newObject bool) (int64, int64, bool, error) {
	var nodeID, versionID int64
	var reused bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		current, err := q.GetUploadSessionForUpdate(ctx, upload.ID)
		if err != nil || current.State != stateFinalizing {
			if err == nil {
				err = errPublishConflict
			}
			return err
		}
		// A file deletion clears the target of a replacement upload and keeps the expected revision.
		if current.ExpectedRevision.Valid && !current.TargetNodeID.Valid {
			return errPublishConflict
		}
		if err := q.LockLibraryByID(ctx, upload.LibraryID); err != nil {
			return err
		}
		blob, err := q.FindBlobByDedupFingerprint(ctx, db.FindBlobByDedupFingerprintParams{
			LibraryID: upload.LibraryID, DedupFingerprint: fingerprint,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// A file deletion removed the matching blob after the first lookup, so the object was not stored.
			if !newObject {
				return errBlobDeleted
			}
			blob, err = q.CreateBlob(ctx, db.CreateBlobParams{
				PublicID: id.New(id.Blob), LibraryID: upload.LibraryID, SizeBytes: upload.DeclaredSize,
				CiphertextSha256: checksum[:], DedupFingerprint: fingerprint,
				EncryptionFormat: upload.EncryptionFormat, EncryptedFileKey: upload.EncryptedFileKey,
			})
			if err != nil {
				return err
			}
			location, err := q.CreateBlobLocation(ctx, db.CreateBlobLocationParams{
				PublicID: id.New(id.BlobLocation), BlobID: blob.ID, LibraryID: upload.LibraryID,
				BackendID: upload.BackendID, ObjectKey: upload.DestinationKey,
			})
			if err != nil {
				return err
			}
			if _, err = q.MarkBlobLocationAvailable(ctx, location.ID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			reused = true
		}
		var node db.Node
		if upload.TargetNodeID.Valid {
			node, err = q.LockNodeForPublication(ctx, upload.TargetNodeID.Int64)
			if err != nil || node.LibraryID != upload.LibraryID || node.Kind != "file" || node.TrashedAt.Valid ||
				node.Revision != upload.ExpectedRevision.Int64 {
				return errPublishConflict
			}
		} else {
			node, err = q.CreateUploadFileNode(ctx, db.CreateUploadFileNodeParams{
				PublicID: id.New(id.Node), LibraryID: upload.LibraryID, ParentID: validInt(upload.ParentID),
				Name: upload.Name, EncryptedName: upload.EncryptedName, NameToken: upload.NameToken,
			})
			if err != nil {
				return classifyPublishError(err)
			}
		}
		ordinal, err := q.NextFileVersionOrdinal(ctx, node.ID)
		if err != nil {
			return err
		}
		version, err := q.CreateFileVersion(ctx, db.CreateFileVersionParams{
			PublicID: id.New(id.FileVersion), NodeID: node.ID, LibraryID: upload.LibraryID,
			Ordinal: int64(ordinal), BlobID: blob.ID, SizeBytes: contentSize,
		})
		if err != nil {
			return err
		}
		if err := q.UpdateNodeCurrentVersion(ctx, db.UpdateNodeCurrentVersionParams{ID: node.ID, CurrentVersionID: validInt(version.ID)}); err != nil {
			return err
		}
		nodeID, versionID = node.ID, version.ID
		return q.CompleteUploadSession(ctx, db.CompleteUploadSessionParams{
			ID: upload.ID, PublishedNodeID: validInt(nodeID), PublishedVersionID: validInt(versionID),
		})
	})
	return nodeID, versionID, reused, err
}

func (s *Service) finishFailedUpload(ctx context.Context, uploadID int64, code, message string) error {
	return s.finishUpload(ctx, uploadID, "failed", code, message)
}

func (s *Service) finishUpload(ctx context.Context, uploadID int64, state, code, message string) error {
	if err := s.queries.FailUploadSession(ctx, db.FailUploadSessionParams{
		ID: uploadID, State: state, FailureCode: textValue(code), FailureMessage: textValue(message),
	}); err != nil {
		return fmt.Errorf("stop upload publication: %w", err)
	}
	return nil
}

func classifyPublishError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errPublishConflict
	}
	return err
}

func equalHash(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

var (
	errPublishConflict = errors.New("upload publication conflict")
	errBlobDeleted     = errors.New("deduplicated blob was deleted before publication")
)

var _ river.JobArgsWithInsertOpts = FinalizeArgs{}
var _ river.Worker[FinalizeArgs] = (*finalizeWorker)(nil)
var _ river.Worker[expireArgs] = (*expireWorker)(nil)
