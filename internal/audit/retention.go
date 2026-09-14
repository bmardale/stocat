package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/bmardale/stocat/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const (
	Retention      = 90 * 24 * time.Hour
	purgeBatchSize = 1000
)

type purgeArgs struct{}

func (purgeArgs) Kind() string { return "purge-audit-events" }

type purgeWorker struct {
	river.WorkerDefaults[purgeArgs]
	queries *db.Queries
}

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[purgeArgs]) error {
	return Purge(ctx, w.queries, time.Now().Add(-Retention))
}

// Purge deletes events older than cutoff in small batches to keep each transaction short.
func Purge(ctx context.Context, queries *db.Queries, cutoff time.Time) error {
	for {
		deleted, err := queries.DeleteExpiredAuditEvents(ctx, db.DeleteExpiredAuditEventsParams{
			Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, BatchSize: purgeBatchSize,
		})
		if err != nil {
			return fmt.Errorf("delete expired audit events: %w", err)
		}
		if deleted < purgeBatchSize {
			return nil
		}
	}
}

// Workers returns a queue registration function that removes expired events each hour.
func Workers(pool *pgxpool.Pool) func(*river.Workers) []*river.PeriodicJob {
	return func(workers *river.Workers) []*river.PeriodicJob {
		river.AddWorker(workers, &purgeWorker{queries: db.New(pool)})
		return []*river.PeriodicJob{
			river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
				return purgeArgs{}, &river.InsertOpts{Queue: "maintenance", MaxAttempts: 8}
			}, &river.PeriodicJobOpts{ID: "purge-audit-events", RunOnStart: true}),
		}
	}
}
