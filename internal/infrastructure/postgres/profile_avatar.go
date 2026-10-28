package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func (r *ProfileRepository) Reserve(ctx context.Context, userID, key, url string) error {
	_, err := r.pool.Exec(ctx, "INSERT INTO profile_avatar_objects(object_key,user_id,photo_url) VALUES($1,$2,$3)", key, userID, url)
	return err
}
func (r *ProfileRepository) Discard(ctx context.Context, key string) error {
	_, err := r.pool.Exec(ctx, "UPDATE profile_avatar_objects SET status='garbage',delete_after=NOW()+INTERVAL '5 minutes' WHERE object_key=$1 AND status='pending'", key)
	return err
}
func (r *ProfileRepository) CommitAvatar(ctx context.Context, userID, key string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockProfile(ctx, tx, userID); err != nil {
		return err
	}
	var url string
	if err = tx.QueryRow(ctx, "SELECT photo_url FROM profile_avatar_objects WHERE object_key=$1 AND user_id=$2 AND status='pending' AND delete_after>NOW() FOR UPDATE", key, userID).Scan(&url); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE profile_avatar_objects SET status='garbage',delete_after=NOW()+INTERVAL '5 minutes' WHERE user_id=$1 AND status='active'", userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET photo_url=$2,updated_at=NOW() WHERE id=$1", userID, url); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE profile_avatar_objects SET status='active' WHERE object_key=$1", key); err != nil {
		return err
	}
	if err = profileAudit(ctx, tx, userID, "profile.avatar.updated", []string{"photoUrl"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *ProfileRepository) CleanupAvatar(ctx context.Context, remove func(context.Context, string) error) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var key string
	err = tx.QueryRow(ctx, "SELECT object_key FROM profile_avatar_objects WHERE status<>'active' AND delete_after<=NOW() AND NOT EXISTS(SELECT 1 FROM users WHERE users.photo_url=profile_avatar_objects.photo_url) ORDER BY delete_after LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	workErr := remove(callCtx, key)
	cancel()
	if workErr == nil {
		_, err = tx.Exec(ctx, "DELETE FROM profile_avatar_objects WHERE object_key=$1", key)
	} else {
		_, err = tx.Exec(ctx, "UPDATE profile_avatar_objects SET attempts=attempts+1,delete_after=NOW()+INTERVAL '1 hour' WHERE object_key=$1", key)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
