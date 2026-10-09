package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InVerein führt fn in einer Transaktion aus, in der app.verein_id gesetzt ist.
// Die RLS-Policies lassen darin nur Zeilen dieses Vereins zu.
func InVerein(ctx context.Context, pool *pgxpool.Pool, vereinID string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.verein_id', $1, true)`, vereinID); err != nil {
			return fmt.Errorf("verein setzen: %w", err)
		}
		return fn(tx)
	})
}
