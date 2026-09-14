// Package replication mirrors one library into another library.
package replication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type LibraryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Replication struct {
	ID           string     `json:"id"`
	Source       LibraryRef `json:"source"`
	Destination  LibraryRef `json:"destination"`
	State        string     `json:"state" enum:"pending,syncing,ready,failed"`
	LastError    string     `json:"last_error,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	stores  *storage.Service
	queue   *river.Client[pgx.Tx]
	log     *slog.Logger
}

type createReplicationInput struct {
	Body struct {
		SourceLibraryID      string `json:"source_library_id" maxLength:"64"`
		DestinationLibraryID string `json:"destination_library_id" maxLength:"64"`
	}
}

type replicationInput struct {
	ID string `path:"id" maxLength:"64"`
}
type replicationOutput struct{ Body Replication }
type replicationsOutput struct {
	Body []Replication `nullable:"false"`
}
type noContentOutput struct {
	Status int `status:"204"`
}

func New(pool *pgxpool.Pool, stores *storage.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), stores: stores, log: logger}
}

func (s *Service) UseQueue(queue *river.Client[pgx.Tx]) { s.queue = queue }

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/replications")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Replications"} })
	huma.Register(group, huma.Operation{
		OperationID: "replications-list", Method: http.MethodGet, Path: "", Summary: "List library replications",
	}, s.list)
	huma.Register(group, huma.Operation{
		OperationID: "replications-create", Method: http.MethodPost, Path: "", Summary: "Create a library replication",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: 65536,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, s.create)
	huma.Register(group, huma.Operation{
		OperationID: "replications-sync", Method: http.MethodPost, Path: "/{id}/sync", Summary: "Sync a library replication",
		DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusNotFound, http.StatusServiceUnavailable},
	}, s.sync)
	huma.Register(group, huma.Operation{
		OperationID: "replications-delete", Method: http.MethodDelete, Path: "/{id}", Summary: "Stop a library replication",
		DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusNotFound},
	}, s.remove)
}

func (s *Service) list(ctx context.Context, _ *struct{}) (*replicationsOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	rows, err := s.queries.ListLibraryReplicationsByOwner(ctx, user.ID)
	if err != nil {
		return nil, s.internalError(ctx, "list replications", err)
	}
	output := &replicationsOutput{Body: make([]Replication, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, fromListRow(row))
	}
	return output, nil
}

func (s *Service) create(ctx context.Context, input *createReplicationInput) (*replicationOutput, error) {
	if input.Body.SourceLibraryID == input.Body.DestinationLibraryID {
		return nil, huma.Error422UnprocessableEntity("Choose two different libraries.")
	}
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("Replication is temporarily unavailable.")
	}
	user, _ := auth.UserFromContext(ctx)
	var loaded db.GetReplicationByPublicIDAndOwnerRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		row, err := queries.CreateLibraryReplication(ctx, db.CreateLibraryReplicationParams{
			PublicID: id.New(id.Replication), OwnerID: user.ID,
			SourcePublicID: input.Body.SourceLibraryID, DestinationPublicID: input.Body.DestinationLibraryID,
		})
		if err != nil {
			return err
		}
		loaded, err = queries.GetReplicationByPublicIDAndOwner(ctx, db.GetReplicationByPublicIDAndOwnerParams{PublicID: row.PublicID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		return s.enqueueAndRecord(ctx, tx, audit.ReplicationCreated, user.ID, loaded)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error422UnprocessableEntity("Use an empty destination with the same encryption mode.")
	}
	if isUniqueViolation(err) {
		return nil, huma.Error409Conflict("One of these libraries already has a replication role.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create replication", err)
	}
	return &replicationOutput{Body: fromGetRow(loaded)}, nil
}

func (s *Service) sync(ctx context.Context, input *replicationInput) (*noContentOutput, error) {
	if s.queue == nil {
		return nil, huma.Error503ServiceUnavailable("Replication is temporarily unavailable.")
	}
	user, _ := auth.UserFromContext(ctx)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := db.New(tx).GetReplicationByPublicIDAndOwner(ctx, db.GetReplicationByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		return s.enqueueAndRecord(ctx, tx, audit.ReplicationSynced, user.ID, row)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The replication does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "start replication", err)
	}
	return &noContentOutput{}, nil
}

func (s *Service) remove(ctx context.Context, input *replicationInput) (*noContentOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		row, err := queries.GetReplicationByPublicIDAndOwner(ctx, db.GetReplicationByPublicIDAndOwnerParams{PublicID: input.ID, OwnerID: user.ID})
		if err != nil {
			return err
		}
		if _, err := queries.DeleteLibraryReplication(ctx, db.DeleteLibraryReplicationParams{PublicID: input.ID, OwnerID: user.ID}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, replicationEvent(audit.ReplicationDeleted, user.ID, row))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The replication does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete replication", err)
	}
	return &noContentOutput{}, nil
}

func (s *Service) enqueue(ctx context.Context, replicationID int64) error {
	_, err := s.queue.Insert(ctx, SyncArgs{ReplicationID: replicationID}, nil)
	return err
}

func (s *Service) enqueueAndRecord(ctx context.Context, tx pgx.Tx, action audit.Action, userID int64, row db.GetReplicationByPublicIDAndOwnerRow) error {
	if _, err := s.queue.InsertTx(ctx, tx, SyncArgs{ReplicationID: row.ID}, nil); err != nil {
		return fmt.Errorf("enqueue replication sync: %w", err)
	}
	return audit.Record(ctx, db.New(tx), replicationEvent(action, userID, row))
}

func replicationEvent(action audit.Action, userID int64, row db.GetReplicationByPublicIDAndOwnerRow) audit.Event {
	return audit.Event{
		Action: action, ActorID: userID, TargetID: row.PublicID,
		Details: audit.Details{
			LibraryID: row.SourcePublicID, LibraryName: row.SourceName,
			DestinationLibraryID: row.DestinationPublicID, DestinationLibraryName: row.DestinationName,
		},
	}
}

func fromListRow(row db.ListLibraryReplicationsByOwnerRow) Replication {
	item := Replication{
		ID: row.PublicID, Source: LibraryRef{ID: row.SourcePublicID, Name: row.SourceName},
		Destination: LibraryRef{ID: row.DestinationPublicID, Name: row.DestinationName},
		State:       row.State, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.LastError.Valid {
		item.LastError = row.LastError.String
	}
	if row.LastSyncedAt.Valid {
		item.LastSyncedAt = &row.LastSyncedAt.Time
	}
	return item
}

func fromGetRow(row db.GetReplicationByPublicIDAndOwnerRow) Replication {
	return fromListRow(db.ListLibraryReplicationsByOwnerRow{
		PublicID: row.PublicID, SourcePublicID: row.SourcePublicID, SourceName: row.SourceName,
		DestinationPublicID: row.DestinationPublicID, DestinationName: row.DestinationName,
		State: row.State, LastError: row.LastError, LastSyncedAt: row.LastSyncedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Replication is unavailable.")
}

func EnsureLibraryWritable(ctx context.Context, queries *db.Queries, libraryID string, ownerID int64) error {
	readOnly, err := queries.IsLibraryReplicationDestination(ctx, db.IsLibraryReplicationDestinationParams{PublicID: libraryID, OwnerID: ownerID})
	if err != nil {
		return err
	}
	if readOnly {
		return huma.Error409Conflict("This library is a read-only replication destination.")
	}
	return nil
}
