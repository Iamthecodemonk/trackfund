package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"time"
	authapp "trackfund/internal/application/auth"
	profileapp "trackfund/internal/application/profile"
)

type ProfileRepository struct{ pool *pgxpool.Pool }

func NewProfileRepository(pool *pgxpool.Pool) *ProfileRepository {
	return &ProfileRepository{pool: pool}
}

type profileQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadProfile(ctx context.Context, q profileQuery, userID string) (profileapp.Profile, error) {
	var p profileapp.Profile
	err := q.QueryRow(ctx, `SELECT u.id,u.full_name,u.email,u.phone,u.photo_url,c.pending_email,c.challenge_id,c.expires_at,u.created_at,u.updated_at
 FROM users u LEFT JOIN profile_email_changes c ON c.user_id=u.id WHERE u.id=$1 AND u.status='active'`, userID).Scan(&p.ID, &p.FullName, &p.Email, &p.PhoneNumber, &p.PhotoURL, &p.PendingEmail, &p.EmailChangeID, &p.EmailChangeExpiresAt, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, profileapp.ErrNotFound
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, err
}
func (r *ProfileRepository) Get(ctx context.Context, userID string) (profileapp.Profile, error) {
	return loadProfile(ctx, r.pool, userID)
}
func (r *ProfileRepository) Allow(ctx context.Context, userID, operation string, max int, window time.Duration) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, `INSERT INTO profile_rate_limits(user_id,operation,request_count) VALUES($1,$2,1)
 ON CONFLICT(user_id,operation) DO UPDATE SET
 request_count=CASE WHEN profile_rate_limits.window_start<=NOW()-($3*INTERVAL '1 second') THEN 1 ELSE LEAST(profile_rate_limits.request_count+1,$4+1) END,
 window_start=CASE WHEN profile_rate_limits.window_start<=NOW()-($3*INTERVAL '1 second') THEN NOW() ELSE profile_rate_limits.window_start END
 RETURNING request_count<=$4`, userID, operation, int(window.Seconds()), max).Scan(&allowed)
	return allowed, err
}
func profileAudit(ctx context.Context, tx pgx.Tx, userID, action string, fields []string) error {
	id, err := authapp.RandomID()
	if err != nil {
		return err
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO profile_audit_events(id,user_id,action,changed_fields) VALUES($1,$2,$3,$4)`, id, userID, action, body)
	return err
}
func lockProfile(ctx context.Context, tx pgx.Tx, userID string) (string, error) {
	var email string
	err := tx.QueryRow(ctx, "SELECT email FROM users WHERE id=$1 AND status='active' FOR UPDATE", userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", profileapp.ErrNotFound
	}
	return email, err
}
func emailConflict(ctx context.Context, tx pgx.Tx, userID, email string) (bool, error) {
	var conflict bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1) AND id<>$2)", email, userID).Scan(&conflict)
	return conflict, err
}
func (r *ProfileRepository) Update(ctx context.Context, userID string, c profileapp.Changes, challenge *profileapp.Challenge) (profileapp.Profile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return profileapp.Profile{}, err
	}
	defer tx.Rollback(ctx)
	email, err := lockProfile(ctx, tx, userID)
	if err != nil {
		return profileapp.Profile{}, err
	}
	if c.PhotoURL.Set {
		var next *string = c.PhotoURL.Value
		if _, err = tx.Exec(ctx, `UPDATE profile_avatar_objects SET status='garbage',delete_after=NOW()+INTERVAL '5 minutes' WHERE user_id=$1 AND status='active' AND photo_url IS DISTINCT FROM $2`, userID, next); err != nil {
			return profileapp.Profile{}, err
		}
	}
	if c.Email.Set {
		if strings.EqualFold(*c.Email.Value, email) {
			if _, err = tx.Exec(ctx, "DELETE FROM profile_email_changes WHERE user_id=$1", userID); err != nil {
				return profileapp.Profile{}, err
			}
		} else {
			conflict, err := emailConflict(ctx, tx, userID, *c.Email.Value)
			if err != nil {
				return profileapp.Profile{}, err
			}
			if conflict {
				return profileapp.Profile{}, profileapp.ErrEmailConflict
			}
			if challenge == nil || challenge.Email != *c.Email.Value {
				return profileapp.Profile{}, fmt.Errorf("missing email challenge")
			}
			_, err = tx.Exec(ctx, `INSERT INTO profile_email_changes(user_id,challenge_id,pending_email,code_hash,expires_at)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id) DO UPDATE SET challenge_id=EXCLUDED.challenge_id,pending_email=EXCLUDED.pending_email,
 code_hash=EXCLUDED.code_hash,expires_at=EXCLUDED.expires_at,attempts=0,created_at=NOW()`, userID, challenge.ID, challenge.Email, challenge.CodeHash, challenge.ExpiresAt)
			if err != nil {
				return profileapp.Profile{}, err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE users SET full_name=CASE WHEN $2 THEN $3 ELSE full_name END,
 phone=CASE WHEN $4 THEN COALESCE($5,'') ELSE phone END,
 photo_url=CASE WHEN $6 THEN $7 ELSE photo_url END,updated_at=NOW() WHERE id=$1`, userID, c.FullName.Set, c.FullName.Value, c.PhoneNumber.Set, c.PhoneNumber.Value, c.PhotoURL.Set, c.PhotoURL.Value)
	if err != nil {
		return profileapp.Profile{}, err
	}
	if err = profileAudit(ctx, tx, userID, "profile.updated", c.Fields()); err != nil {
		return profileapp.Profile{}, err
	}
	result, err := loadProfile(ctx, tx, userID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (r *ProfileRepository) CancelChallenge(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM profile_email_changes WHERE user_id=$1 AND challenge_id=$2", userID, id)
	return err
}
func (r *ProfileRepository) Verify(ctx context.Context, userID, id, digest string) (profileapp.Profile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return profileapp.Profile{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = lockProfile(ctx, tx, userID); err != nil {
		return profileapp.Profile{}, err
	}
	var challenge, hash, email string
	var expired bool
	var attempts int
	err = tx.QueryRow(ctx, `SELECT challenge_id,code_hash,pending_email,expires_at<=NOW(),attempts FROM profile_email_changes WHERE user_id=$1 FOR UPDATE`, userID).Scan(&challenge, &hash, &email, &expired, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return profileapp.Profile{}, profileapp.ErrInvalidCode
	}
	if err != nil {
		return profileapp.Profile{}, err
	}
	if challenge != id || expired {
		return profileapp.Profile{}, profileapp.ErrInvalidCode
	}
	if attempts >= 5 {
		return profileapp.Profile{}, profileapp.ErrAttemptsExhausted
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(digest)) != 1 {
		if _, err = tx.Exec(ctx, "UPDATE profile_email_changes SET attempts=attempts+1 WHERE user_id=$1", userID); err != nil {
			return profileapp.Profile{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return profileapp.Profile{}, err
		}
		return profileapp.Profile{}, profileapp.ErrInvalidCode
	}
	conflict, err := emailConflict(ctx, tx, userID, email)
	if err != nil {
		return profileapp.Profile{}, err
	}
	if conflict {
		return profileapp.Profile{}, profileapp.ErrEmailConflict
	}
	_, err = tx.Exec(ctx, "UPDATE users SET email=$2,is_email_verified=true,token_version=token_version+1,updated_at=NOW() WHERE id=$1", userID, email)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return profileapp.Profile{}, profileapp.ErrEmailConflict
		}
		return profileapp.Profile{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE refresh_tokens SET revoked_at=COALESCE(revoked_at,NOW()) WHERE user_id=$1", userID); err != nil {
		return profileapp.Profile{}, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM otps WHERE user_id=$1", userID); err != nil {
		return profileapp.Profile{}, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM profile_email_changes WHERE user_id=$1", userID); err != nil {
		return profileapp.Profile{}, err
	}
	if err = profileAudit(ctx, tx, userID, "profile.email.verified", []string{"email"}); err != nil {
		return profileapp.Profile{}, err
	}
	result, err := loadProfile(ctx, tx, userID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
