package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx commits if fn succeeds and rolls back if fn fails.
// Use the supplied queries only within fn.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error {
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return fn(New(tx))
	})
	if err != nil {
		return fmt.Errorf("database transaction: %w", err)
	}
	return nil
}
