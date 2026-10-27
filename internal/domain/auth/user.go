package auth

import "time"

type Role string

const (
	RoleUser       Role = "USER"
	RoleAdmin      Role = "ADMIN"
	RoleSuperAdmin Role = "SUPER_ADMIN"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusDeleted   Status = "deleted"
)

type OTPType string

const (
	OTPTypeEmailVerification OTPType = "email_verification"
	OTPTypePasswordReset     OTPType = "password_reset"
)

type User struct {
	ID                string
	FullName          string
	Email             string
	Phone             string
	PasswordHash      string
	Role              Role
	Status            Status
	IsEmailVerified   bool
	TokenVersion      int
	LastLoginAt       *time.Time
	PasswordChangedAt *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RefreshToken struct {
	TokenVersion *int
	ID           string
	UserID       string
	TokenHash    string
	DeviceName   string
	IPAddress    string
	UserAgent    string
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	CreatedAt    time.Time
}

type OTP struct {
	ID        string
	UserID    string
	Email     string
	OTPHash   string
	Type      OTPType
	ExpiresAt time.Time
	Attempts  int
	CreatedAt time.Time
}
