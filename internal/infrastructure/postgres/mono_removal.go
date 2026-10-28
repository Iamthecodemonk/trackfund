package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// DeleteAccount atomically hides a link and schedules provider revocation. Imported rows
// are retained for audit but all user-facing reads reject the tombstoned account.
func (r *MonoRepository) DeleteAccount(ctx context.Context, id, userID string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var monoID, owner string
	err = tx.QueryRow(ctx, "SELECT mono_account_id,user_id FROM mono_accounts WHERE id=$1", id).Scan(&monoID, &owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "mono-account:"+monoID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "UPDATE mono_accounts SET deleted_at=COALESCE(deleted_at,NOW()),updated_at=NOW() WHERE id=$1", id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "UPDATE mono_link_sessions SET status='failed',updated_at=NOW() WHERE mono_account_id=$1", monoID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO mono_unlink_jobs(mono_account_id) VALUES($1) ON CONFLICT DO NOTHING", monoID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Claim a durable job. The job row lock prevents parallel unlinks across API instances.
func (r *MonoRepository) ProcessUnlink(ctx context.Context, unlink func(context.Context, string) error) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, "SELECT mono_account_id FROM mono_unlink_jobs WHERE status='pending' AND next_attempt_at<=NOW() ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	workErr := unlink(callCtx, id)
	cancel()
	if workErr == nil {
		_, err = tx.Exec(ctx, "UPDATE mono_unlink_jobs SET status='done',attempts=attempts+1,completed_at=NOW(),last_error_code=NULL WHERE mono_account_id=$1", id)
	} else {
		_, err = tx.Exec(ctx, "UPDATE mono_unlink_jobs SET attempts=attempts+1,status=CASE WHEN attempts+1>=20 THEN 'failed' ELSE 'pending' END,last_error_code='provider_unlink_failed',next_attempt_at=NOW()+LEAST(3600,POWER(2,LEAST(attempts+1,12)))*INTERVAL '1 second' WHERE mono_account_id=$1", id)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (r *MonoRepository) MarkLinkSessionFailed(ctx context.Context, reference string) error {
	_, err := r.pool.Exec(ctx, "UPDATE mono_link_sessions SET status='failed',updated_at=NOW() WHERE reference=$1 AND status='pending'", reference)
	return err
}
