package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trackfund/internal/domain/auth"
)

type AuthRepository struct {
	pool *pgxpool.Pool
}

func NewAuthRepository(pool *pgxpool.Pool) *AuthRepository {
	return &AuthRepository{pool: pool}
}

func (r *AuthRepository) CreateUser(ctx context.Context, user auth.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, full_name, email, phone, password_hash, role, status, is_email_verified, token_version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		user.ID, user.FullName, user.Email, user.Phone, user.PasswordHash, user.Role, user.Status, user.IsEmailVerified, user.TokenVersion, user.CreatedAt, user.UpdatedAt)
	return err
}

func (r *AuthRepository) FindUserByEmail(ctx context.Context, email string) (auth.User, bool, error) {
	return r.findUser(ctx, `SELECT id, full_name, email, phone, password_hash, role, status, is_email_verified, token_version, last_login_at, password_changed_at, created_at, updated_at FROM users WHERE email = $1`, email)
}

func (r *AuthRepository) FindUserByID(ctx context.Context, id string) (auth.User, bool, error) {
	return r.findUser(ctx, `SELECT id, full_name, email, phone, password_hash, role, status, is_email_verified, token_version, last_login_at, password_changed_at, created_at, updated_at FROM users WHERE id = $1`, id)
}

func (r *AuthRepository) ActivateUser(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET status = 'active', is_email_verified = true, updated_at = NOW() WHERE id = $1`, userID)
	return err
}

func (r *AuthRepository) UpdateLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET last_login_at = $2, updated_at = $2 WHERE id = $1`, userID, at)
	return err
}

func (r *AuthRepository) UpdatePassword(ctx context.Context, userID, passwordHash string, changedAt time.Time, tokenVersion int) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $2, password_changed_at = $3, token_version = $4, updated_at = $3 WHERE id = $1`, userID, passwordHash, changedAt, tokenVersion)
	return err
}

func (r *AuthRepository) UpdateTokenVersion(ctx context.Context, userID string, tokenVersion int, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET token_version = $2, updated_at = $3 WHERE id = $1`, userID, tokenVersion, at)
	return err
}

func (r *AuthRepository) SaveOTP(ctx context.Context, otp auth.OTP) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO otps (id, user_id, email, otp_hash, type, expires_at, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		otp.ID, otp.UserID, otp.Email, otp.OTPHash, otp.Type, otp.ExpiresAt, otp.Attempts, otp.CreatedAt)
	return err
}

func (r *AuthRepository) FindOTP(ctx context.Context, email string, typ auth.OTPType) (auth.OTP, bool, error) {
	var otp auth.OTP
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, email, otp_hash, type, expires_at, attempts, created_at
		FROM otps
		WHERE email = $1 AND type = $2
		ORDER BY created_at DESC
		LIMIT 1`, email, typ).Scan(&otp.ID, &otp.UserID, &otp.Email, &otp.OTPHash, &otp.Type, &otp.ExpiresAt, &otp.Attempts, &otp.CreatedAt)
	if err == pgx.ErrNoRows {
		return auth.OTP{}, false, nil
	}
	return otp, err == nil, err
}

func (r *AuthRepository) DeleteOTP(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM otps WHERE id = $1`, id)
	return err
}

func (r *AuthRepository) CountRecentOTPs(ctx context.Context, email string, typ auth.OTPType, since time.Time) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM otps WHERE email = $1 AND type = $2 AND created_at >= $3`, email, typ, since).Scan(&count)
	return count, err
}

func (r *AuthRepository) IncrementOTPAttempts(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE otps SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}

func (r *AuthRepository) SaveRefreshToken(ctx context.Context, token auth.RefreshToken) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, device_name, ip_address, user_agent, expires_at, revoked_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		token.ID, token.UserID, token.TokenHash, token.DeviceName, token.IPAddress, token.UserAgent, token.ExpiresAt, token.RevokedAt, token.CreatedAt)
	return err
}

func (r *AuthRepository) FindRefreshToken(ctx context.Context, tokenHash string) (auth.RefreshToken, bool, error) {
	var token auth.RefreshToken
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, token_hash, device_name, ip_address, user_agent, expires_at, revoked_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1`, tokenHash).Scan(&token.ID, &token.UserID, &token.TokenHash, &token.DeviceName, &token.IPAddress, &token.UserAgent, &token.ExpiresAt, &token.RevokedAt, &token.CreatedAt)
	if err == pgx.ErrNoRows {
		return auth.RefreshToken{}, false, nil
	}
	return token, err == nil, err
}

func (r *AuthRepository) RevokeRefreshToken(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, at)
	return err
}

func (r *AuthRepository) RevokeUserRefreshTokens(ctx context.Context, userID string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, $2) WHERE user_id = $1`, userID, at)
	return err
}

func (r *AuthRepository) findUser(ctx context.Context, query string, arg string) (auth.User, bool, error) {
	var user auth.User
	err := r.pool.QueryRow(ctx, query, arg).Scan(
		&user.ID,
		&user.FullName,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.Role,
		&user.Status,
		&user.IsEmailVerified,
		&user.TokenVersion,
		&user.LastLoginAt,
		&user.PasswordChangedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return auth.User{}, false, nil
	}
	return user, err == nil, err
}
