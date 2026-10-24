package authapp

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"trackfund/internal/domain/auth"
)

var (
	ErrInvalidCredentials = errors.New("Invalid email or password.")
	ErrEmailExists        = errors.New("email already exists")
	ErrEmailNotVerified   = errors.New("Please verify your email before logging in.")
	ErrAccountRestricted  = errors.New("Your account is currently restricted. Please contact support.")
	ErrInvalidOTP         = errors.New("Invalid or expired OTP. Please request a new one.")
	ErrTooManyOTPRequests = errors.New("Too many OTP requests. Please try again later.")
	ErrSessionExpired     = errors.New("Session expired. Please login again.")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrWeakPassword       = errors.New("password must be at least 8 characters and include upper, lower, number, and symbol")
)

type Repository interface {
	CreateUser(ctx context.Context, user auth.User) error
	FindUserByEmail(ctx context.Context, email string) (auth.User, bool, error)
	FindUserByID(ctx context.Context, id string) (auth.User, bool, error)
	ActivateUser(ctx context.Context, userID string) error
	UpdateLastLogin(ctx context.Context, userID string, at time.Time) error
	UpdatePassword(ctx context.Context, userID, passwordHash string, changedAt time.Time, tokenVersion int) error
	UpdateTokenVersion(ctx context.Context, userID string, tokenVersion int, at time.Time) error

	SaveOTP(ctx context.Context, otp auth.OTP) error
	FindOTP(ctx context.Context, email string, typ auth.OTPType) (auth.OTP, bool, error)
	DeleteOTP(ctx context.Context, id string) error
	CountRecentOTPs(ctx context.Context, email string, typ auth.OTPType, since time.Time) (int, error)
	IncrementOTPAttempts(ctx context.Context, id string) error

	SaveRefreshToken(ctx context.Context, token auth.RefreshToken) error
	FindRefreshToken(ctx context.Context, tokenHash string) (auth.RefreshToken, bool, error)
	RevokeRefreshToken(ctx context.Context, id string, at time.Time) error
	RevokeUserRefreshTokens(ctx context.Context, userID string, at time.Time) error

	// pending users (saved during registration until OTP confirmation)
	SavePendingUser(ctx context.Context, user auth.User) error
	FindPendingUserByEmail(ctx context.Context, email string) (auth.User, bool, error)
	DeletePendingUser(ctx context.Context, id string) error
	// OAuth providers linking
	FindUserByOAuth(ctx context.Context, provider string, providerID string) (auth.User, bool, error)
	SaveOAuthProvider(ctx context.Context, provider string, providerID string, userID string) error
}

// Add oauth lookups

type EmailSender interface {
	SendOTP(ctx context.Context, email, otp string, typ auth.OTPType) error
}

type Service struct {
	repo              Repository
	emailSender       EmailSender
	passwordHasher    PasswordHasher
	tokenManager      TokenManager
	secret            string
	refreshTokenTTL   time.Duration
	otpTTL            time.Duration
	otpRateLimitCount int
	otpRateLimitEvery time.Duration
	now               func() time.Time
}

type Config struct {
	JWTSecret         string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	OTPTTL            time.Duration
	OTPRateLimitCount int
	OTPRateLimitEvery time.Duration
}

func NewService(repo Repository, emailSender EmailSender, cfg Config) Service {
	if cfg.AccessTokenTTL == 0 {
		cfg.AccessTokenTTL = 15 * time.Minute
	}
	if cfg.RefreshTokenTTL == 0 {
		cfg.RefreshTokenTTL = 30 * 24 * time.Hour
	}
	if cfg.OTPTTL == 0 {
		cfg.OTPTTL = 10 * time.Minute
	}
	if cfg.OTPRateLimitCount == 0 {
		cfg.OTPRateLimitCount = 3
	}
	if cfg.OTPRateLimitEvery == 0 {
		cfg.OTPRateLimitEvery = time.Hour
	}
	return Service{
		repo:              repo,
		emailSender:       emailSender,
		passwordHasher:    PasswordHasher{},
		tokenManager:      NewTokenManager(cfg.JWTSecret, "trackfund", cfg.AccessTokenTTL),
		secret:            cfg.JWTSecret,
		refreshTokenTTL:   cfg.RefreshTokenTTL,
		otpTTL:            cfg.OTPTTL,
		otpRateLimitCount: cfg.OTPRateLimitCount,
		otpRateLimitEvery: cfg.OTPRateLimitEvery,
		now:               func() time.Time { return time.Now().UTC() },
	}
}

type RegisterCommand struct {
	FullName string `json:"fullName"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type LoginCommand struct {
	Email      string
	Password   string
	DeviceName string
	IPAddress  string
	UserAgent  string
}

type AuthResult struct {
	User         UserDTO `json:"user"`
	AccessToken  string  `json:"accessToken"`
	RefreshToken string  `json:"refreshToken"`
}

type UserDTO struct {
	ID       string `json:"id"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

func (s Service) Register(ctx context.Context, command RegisterCommand) error {
	email := normalizeEmail(command.Email)
	if strings.TrimSpace(command.FullName) == "" || email == "" || !validPassword(command.Password) {
		if !validPassword(command.Password) {
			return ErrWeakPassword
		}
		return errors.New("fullName, email, and password are required")
	}
	if _, ok, err := s.repo.FindUserByEmail(ctx, email); err != nil {
		return err
	} else if ok {
		return ErrEmailExists
	}
	passwordHash, err := s.passwordHasher.Hash(command.Password)
	if err != nil {
		return err
	}
	userID, err := randomID()
	if err != nil {
		return err
	}
	now := s.now()
	// save as pending registration until OTP confirmed
	pending := auth.User{
		ID:              userID,
		FullName:        strings.TrimSpace(command.FullName),
		Email:           email,
		Phone:           strings.TrimSpace(command.Phone),
		PasswordHash:    passwordHash,
		Role:            auth.RoleUser,
		Status:          auth.StatusPending,
		IsEmailVerified: false,
		TokenVersion:    0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	// if there's an existing pending registration for this email, remove it first
	if existing, ok, err := s.repo.FindPendingUserByEmail(ctx, pending.Email); err != nil {
		return err
	} else if ok {
		if err := s.repo.DeletePendingUser(ctx, existing.ID); err != nil {
			return err
		}
	}

	if err := s.repo.SavePendingUser(ctx, pending); err != nil {
		return err
	}
	return s.issueOTP(ctx, pending.ID, pending.Email, auth.OTPTypeEmailVerification)
}

func (s Service) VerifyOTP(ctx context.Context, email, otpValue string) error {
	normalized := normalizeEmail(email)
	// first check pending registrations
	pending, ok, err := s.repo.FindPendingUserByEmail(ctx, normalized)
	if err != nil {
		return err
	}
	if ok {
		otp, err := s.verifyOTP(ctx, pending.Email, otpValue, auth.OTPTypeEmailVerification)
		if err != nil {
			return err
		}
		// create the real user
		// mark as active and verified before creating in users table
		pending.Status = auth.StatusActive
		pending.IsEmailVerified = true
		pending.UpdatedAt = s.now()
		if err := s.repo.CreateUser(ctx, pending); err != nil {
			return err
		}
		if err := s.repo.DeletePendingUser(ctx, pending.ID); err != nil {
			return err
		}
		return s.repo.DeleteOTP(ctx, otp.ID)
	}

	// fallback: existing user flow
	user, ok, err := s.repo.FindUserByEmail(ctx, normalized)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidOTP
	}
	otp, err := s.verifyOTP(ctx, user.Email, otpValue, auth.OTPTypeEmailVerification)
	if err != nil {
		return err
	}
	if err := s.repo.ActivateUser(ctx, user.ID); err != nil {
		return err
	}
	return s.repo.DeleteOTP(ctx, otp.ID)
}

func (s Service) ResendOTP(ctx context.Context, email string) error {
	user, ok, err := s.repo.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.issueOTP(ctx, user.ID, user.Email, auth.OTPTypeEmailVerification)
}

func (s Service) Login(ctx context.Context, command LoginCommand) (AuthResult, error) {
	user, ok, err := s.repo.FindUserByEmail(ctx, normalizeEmail(command.Email))
	if err != nil {
		return AuthResult{}, err
	}
	if !ok || !s.passwordHasher.Verify(command.Password, user.PasswordHash) {
		return AuthResult{}, ErrInvalidCredentials
	}
	if !user.IsEmailVerified {
		return AuthResult{}, ErrEmailNotVerified
	}
	if user.Status != auth.StatusActive {
		return AuthResult{}, ErrAccountRestricted
	}
	result, err := s.createSession(ctx, user, command.DeviceName, command.IPAddress, command.UserAgent)
	if err != nil {
		return AuthResult{}, err
	}
	_ = s.repo.UpdateLastLogin(ctx, user.ID, s.now())
	return result, nil
}

func (s Service) Refresh(ctx context.Context, refreshToken, deviceName, ipAddress, userAgent string) (AuthResult, error) {
	stored, ok, err := s.repo.FindRefreshToken(ctx, keyedHash(s.secret, refreshToken))
	if err != nil {
		return AuthResult{}, err
	}
	if !ok || stored.RevokedAt != nil || stored.ExpiresAt.Before(s.now()) {
		return AuthResult{}, ErrSessionExpired
	}
	user, ok, err := s.repo.FindUserByID(ctx, stored.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	if !ok || user.Status != auth.StatusActive || !user.IsEmailVerified {
		return AuthResult{}, ErrSessionExpired
	}
	if err := s.repo.RevokeRefreshToken(ctx, stored.ID, s.now()); err != nil {
		return AuthResult{}, err
	}
	return s.createSession(ctx, user, deviceName, ipAddress, userAgent)
}

func (s Service) ForgotPassword(ctx context.Context, email string) error {
	user, ok, err := s.repo.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil || !ok {
		return err
	}
	return s.issueOTP(ctx, user.ID, user.Email, auth.OTPTypePasswordReset)
}

func (s Service) ResetPassword(ctx context.Context, email, otpValue, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrWeakPassword
	}
	user, ok, err := s.repo.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidOTP
	}
	otp, err := s.verifyOTP(ctx, user.Email, otpValue, auth.OTPTypePasswordReset)
	if err != nil {
		return err
	}
	return s.changePassword(ctx, user, newPassword, otp.ID)
}

func (s Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrWeakPassword
	}
	user, ok, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !ok || !s.passwordHasher.Verify(currentPassword, user.PasswordHash) {
		return ErrInvalidCredentials
	}
	return s.changePassword(ctx, user, newPassword, "")
}

func (s Service) Logout(ctx context.Context, refreshToken string) error {
	stored, ok, err := s.repo.FindRefreshToken(ctx, keyedHash(s.secret, refreshToken))
	if err != nil || !ok {
		return err
	}
	return s.repo.RevokeRefreshToken(ctx, stored.ID, s.now())
}

func (s Service) LogoutAll(ctx context.Context, userID string) error {
	user, ok, err := s.repo.FindUserByID(ctx, userID)
	if err != nil || !ok {
		return err
	}
	now := s.now()
	if err := s.repo.RevokeUserRefreshTokens(ctx, user.ID, now); err != nil {
		return err
	}
	return s.repo.UpdateTokenVersion(ctx, user.ID, user.TokenVersion+1, now)
}

func (s Service) AuthenticateAccessToken(ctx context.Context, token string) (auth.User, error) {
	claims, err := s.tokenManager.Verify(token)
	if err != nil {
		return auth.User{}, ErrUnauthorized
	}
	user, ok, err := s.repo.FindUserByID(ctx, claims.UserID)
	if err != nil {
		return auth.User{}, err
	}
	if !ok || user.Status != auth.StatusActive || !user.IsEmailVerified || user.TokenVersion != claims.TokenVersion {
		return auth.User{}, ErrUnauthorized
	}
	return user, nil
}

func (s Service) createSession(ctx context.Context, user auth.User, deviceName, ipAddress, userAgent string) (AuthResult, error) {
	accessToken, err := s.tokenManager.Generate(user.ID, string(user.Role), user.TokenVersion)
	if err != nil {
		return AuthResult{}, err
	}
	refreshToken, err := randomToken()
	if err != nil {
		return AuthResult{}, err
	}
	tokenID, err := randomID()
	if err != nil {
		return AuthResult{}, err
	}
	now := s.now()
	if err := s.repo.SaveRefreshToken(ctx, auth.RefreshToken{
		ID:         tokenID,
		UserID:     user.ID,
		TokenHash:  keyedHash(s.secret, refreshToken),
		DeviceName: strings.TrimSpace(deviceName),
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		ExpiresAt:  now.Add(s.refreshTokenTTL),
		CreatedAt:  now,
	}); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		User:         toDTO(user),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s Service) issueOTP(ctx context.Context, userID, email string, typ auth.OTPType) error {
	count, err := s.repo.CountRecentOTPs(ctx, email, typ, s.now().Add(-s.otpRateLimitEvery))
	if err != nil {
		return err
	}
	if count >= s.otpRateLimitCount {
		return ErrTooManyOTPRequests
	}
	otpValue, err := randomOTP()
	if err != nil {
		return err
	}
	otpID, err := randomID()
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.repo.SaveOTP(ctx, auth.OTP{
		ID:        otpID,
		UserID:    userID,
		Email:     email,
		OTPHash:   keyedHash(s.secret, otpValue),
		Type:      typ,
		ExpiresAt: now.Add(s.otpTTL),
		CreatedAt: now,
	}); err != nil {
		return err
	}
	return s.emailSender.SendOTP(ctx, email, otpValue, typ)
}

func (s Service) verifyOTP(ctx context.Context, email, otpValue string, typ auth.OTPType) (auth.OTP, error) {
	otp, ok, err := s.repo.FindOTP(ctx, normalizeEmail(email), typ)
	if err != nil {
		return auth.OTP{}, err
	}
	if !ok || otp.ExpiresAt.Before(s.now()) || otp.OTPHash != keyedHash(s.secret, strings.TrimSpace(otpValue)) {
		if ok {
			_ = s.repo.IncrementOTPAttempts(ctx, otp.ID)
		}
		return auth.OTP{}, ErrInvalidOTP
	}
	return otp, nil
}

// --- OAuth / helper wrappers exposed on Service ---
func (s Service) FindUserByOAuth(ctx context.Context, provider string, providerID string) (auth.User, bool, error) {
	return s.repo.FindUserByOAuth(ctx, provider, providerID)
}

func (s Service) SaveOAuthProvider(ctx context.Context, provider string, providerID string, userID string) error {
	return s.repo.SaveOAuthProvider(ctx, provider, providerID, userID)
}

func (s Service) FindUserByEmail(ctx context.Context, email string) (auth.User, bool, error) {
	return s.repo.FindUserByEmail(ctx, email)
}

func (s Service) CreateUser(ctx context.Context, user auth.User) error {
	return s.repo.CreateUser(ctx, user)
}

// CreateTokensForUser issues access and refresh tokens for an existing user (no password required)
func (s Service) CreateTokensForUser(ctx context.Context, user auth.User) (AuthResult, error) {
	// create session with empty device info
	res, err := s.createSession(ctx, user, "", "", "")
	if err != nil {
		return AuthResult{}, err
	}
	return res, nil
}

func (s Service) changePassword(ctx context.Context, user auth.User, newPassword, otpID string) error {
	passwordHash, err := s.passwordHasher.Hash(newPassword)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.repo.UpdatePassword(ctx, user.ID, passwordHash, now, user.TokenVersion+1); err != nil {
		return err
	}
	if err := s.repo.RevokeUserRefreshTokens(ctx, user.ID, now); err != nil {
		return err
	}
	if otpID != "" {
		return s.repo.DeleteOTP(ctx, otpID)
	}
	return nil
}

func toDTO(user auth.User) UserDTO {
	return UserDTO{ID: user.ID, FullName: user.FullName, Email: user.Email, Role: string(user.Role)}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validPassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	hasLower := regexp.MustCompile(`[a-z]`).MatchString(password)
	hasUpper := regexp.MustCompile(`[A-Z]`).MatchString(password)
	hasNumber := regexp.MustCompile(`[0-9]`).MatchString(password)
	hasSymbol := regexp.MustCompile(`[^a-zA-Z0-9]`).MatchString(password)
	common := map[string]bool{"password": true, "password123": true, "12345678": true}
	return hasLower && hasUpper && hasNumber && hasSymbol && !common[strings.ToLower(password)]
}
