package postgres

import (
	"context"
	"time"
)

func (r *MonoRepository) MarkTransactionsSynced(ctx context.Context, id string, started time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE mono_accounts SET last_synced_at=GREATEST(last_synced_at,$2) WHERE id=$1 AND deleted_at IS NULL`, id, started)
	return err
}
