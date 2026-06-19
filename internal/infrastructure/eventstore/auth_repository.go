package eventstore

import (
	"context"
	"sync"
	"time"

	"trackfund/internal/domain/auth"
)

type InMemoryAuthRepository struct {
	mu            sync.Mutex
	users         map[string]auth.User
	usersByEmail  map[string]string
	otps          map[string]auth.OTP
	refreshTokens map[string]auth.RefreshToken
}

func NewInMemoryAuthRepository() *InMemoryAuthRepository {
	return &InMemoryAuthRepository{
		users:         map[string]auth.User{},
		usersByEmail:  map[string]string{},
		otps:          map[string]auth.OTP{},
		refreshTokens: map[string]auth.RefreshToken{},
	}
}

func (r *InMemoryAuthRepository) CreateUser(_ context.Context, user auth.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[user.ID] = user
	r.usersByEmail[user.Email] = user.ID
	return nil
}

func (r *InMemoryAuthRepository) FindUserByEmail(_ context.Context, email string) (auth.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.usersByEmail[email]
	if !ok {
		return auth.User{}, false, nil
	}
	return r.users[id], true, nil
}

func (r *InMemoryAuthRepository) FindUserByID(_ context.Context, id string) (auth.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.users[id]
	return user, ok, nil
}

func (r *InMemoryAuthRepository) ActivateUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.users[userID]
	user.Status = auth.StatusActive
	user.IsEmailVerified = true
	user.UpdatedAt = time.Now().UTC()
	r.users[userID] = user
	return nil
}

func (r *InMemoryAuthRepository) UpdateLastLogin(_ context.Context, userID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.users[userID]
	user.LastLoginAt = &at
	user.UpdatedAt = at
	r.users[userID] = user
	return nil
}

func (r *InMemoryAuthRepository) UpdatePassword(_ context.Context, userID, passwordHash string, changedAt time.Time, tokenVersion int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.users[userID]
	user.PasswordHash = passwordHash
	user.PasswordChangedAt = &changedAt
	user.TokenVersion = tokenVersion
	user.UpdatedAt = changedAt
	r.users[userID] = user
	return nil
}

func (r *InMemoryAuthRepository) UpdateTokenVersion(_ context.Context, userID string, tokenVersion int, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.users[userID]
	user.TokenVersion = tokenVersion
	user.UpdatedAt = at
	r.users[userID] = user
	return nil
}

func (r *InMemoryAuthRepository) SaveOTP(_ context.Context, otp auth.OTP) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.otps[otp.ID] = otp
	return nil
}

func (r *InMemoryAuthRepository) FindOTP(_ context.Context, email string, typ auth.OTPType) (auth.OTP, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest auth.OTP
	found := false
	for _, otp := range r.otps {
		if otp.Email == email && otp.Type == typ {
			if !found || otp.CreatedAt.After(latest.CreatedAt) {
				latest = otp
				found = true
			}
		}
	}
	return latest, found, nil
}

func (r *InMemoryAuthRepository) DeleteOTP(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.otps, id)
	return nil
}

func (r *InMemoryAuthRepository) CountRecentOTPs(_ context.Context, email string, typ auth.OTPType, since time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, otp := range r.otps {
		if otp.Email == email && otp.Type == typ && otp.CreatedAt.After(since) {
			count++
		}
	}
	return count, nil
}

func (r *InMemoryAuthRepository) IncrementOTPAttempts(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	otp := r.otps[id]
	otp.Attempts++
	r.otps[id] = otp
	return nil
}

func (r *InMemoryAuthRepository) SaveRefreshToken(_ context.Context, token auth.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshTokens[token.TokenHash] = token
	return nil
}

func (r *InMemoryAuthRepository) FindRefreshToken(_ context.Context, tokenHash string) (auth.RefreshToken, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.refreshTokens[tokenHash]
	return token, ok, nil
}

func (r *InMemoryAuthRepository) RevokeRefreshToken(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, token := range r.refreshTokens {
		if token.ID == id {
			token.RevokedAt = &at
			r.refreshTokens[hash] = token
			return nil
		}
	}
	return nil
}

func (r *InMemoryAuthRepository) RevokeUserRefreshTokens(_ context.Context, userID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, token := range r.refreshTokens {
		if token.UserID == userID && token.RevokedAt == nil {
			token.RevokedAt = &at
			r.refreshTokens[hash] = token
		}
	}
	return nil
}
