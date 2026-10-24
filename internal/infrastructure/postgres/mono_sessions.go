package postgres

import "context"

func (r *MonoRepository) CreateLinkSession(ctx context.Context, reference, userID, redirectURL string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO mono_link_sessions(reference,user_id,redirect_url) VALUES ($1,$2,$3)`, reference, userID, redirectURL)
	return err
}
