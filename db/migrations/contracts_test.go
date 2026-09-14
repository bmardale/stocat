package migrations_test

import (
	"embed"
	"strings"
	"testing"

	"github.com/bmardale/stocat/db/migrations"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/testutil"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed testdata/*.sql
var fixtures embed.FS

func TestEncryptedSharingConstraints(t *testing.T) {
	t.Parallel()
	pool := testutil.NewPostgres(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	sql, err := fixtures.ReadFile("testdata/encrypted_sharing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), string(sql)); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedSharingMigrationDownUp(t *testing.T) {
	t.Parallel()
	pool := testutil.NewPostgres(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(t.Context(), 15); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	deployment, err := db.New(pool).GetEncryptionDeployment(t.Context())
	if err != nil || !deployment.Valid {
		t.Fatalf("deployment=%v err=%v", deployment, err)
	}
	again, err := db.New(pool).GetEncryptionDeployment(t.Context())
	if err != nil || deployment != again {
		t.Fatalf("deployment changed: %v", err)
	}
}

func TestEncryptedSharingMigrationPreservesV2Data(t *testing.T) {
	t.Parallel()
	pool := testutil.NewPostgres(t)
	sql, err := fixtures.ReadFile("testdata/encrypted_sharing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(sql)); err != nil {
		t.Fatal(err)
	}
	deployment, err := db.New(pool).GetEncryptionDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(t.Context(), 15); err == nil || !strings.Contains(err.Error(), "V2 data prevents rollback") {
		t.Fatalf("rollback error=%v", err)
	}
	again, err := db.New(pool).GetEncryptionDeployment(t.Context())
	if err != nil || deployment != again {
		t.Fatalf("deployment changed: %v", err)
	}
}
