// Package testutil provides database helpers for integration tests.
package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/bmardale/stocat/db/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewPostgres returns an isolated, migrated database pool and registers cleanup.
// Tests skip in short mode and require Docker otherwise.
func NewPostgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("PostgreSQL integration tests require Docker; omit -short to run them")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("stocat_test"),
		postgres.WithUsername("stocat"),
		postgres.WithPassword("stocat"),
		postgres.BasicWaitStrategies(),
	)
	if container != nil {
		testcontainers.CleanupContainer(t, container)
	}
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("create database pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close migration connection: %v", err)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply database migrations: %v", err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}

	return pool
}
