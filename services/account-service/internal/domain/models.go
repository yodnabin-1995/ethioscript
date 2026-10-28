// Package domain holds all account-service core entities and value objects.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ─── Enums ────────────────────────────────────────────────────────────────────

type UserRole string

const (
	RoleUser  UserRole = "USER"
	RoleAdmin UserRole = "ADMIN"
)

type UserStatus string

const (
	StatusActive              UserStatus = "ACTIVE"
	StatusSuspended           UserStatus = "SUSPENDED"
	StatusPendingVerification UserStatus = "PENDING_VERIFICATION"
	StatusDeleted             UserStatus = "DELETED"
)

// ─── Domain errors ────────────────────────────────────────────────────────────

var (
	ErrNotFound          = errors.New("not found")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("conflict")
	ErrValidation        = errors.New("validation error")
	ErrAccountSuspended  = errors.New("account suspended")
	ErrTokenExpired      = errors.New("token expired or revoked")
	ErrWrongPassword     = errors.New("invalid credentials")
	ErrPasswordPolicy    = errors.New("password does not meet policy requirements")
	ErrEmailNotVerified  = errors.New("email address not verified")
)

// ─── Tenant ───────────────────────────────────────────────────────────────────

type Tenant struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	MaxUploadSizeMB  int       `json:"max_upload_size_mb"`
	MaxStorageGB     int       `json:"max_storage_gb"`
	RetentionDays    int       `json:"retention_days"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ─── User ─────────────────────────────────────────────────────────────────────

type User struct {
	ID                   uuid.UUID  `json:"id"`
	TenantID             uuid.UUID  `json:"tenant_id"`
	Email                string     `json:"email"`
	EmailVerified        bool       `json:"email_verified"`
	Name                 string     `json:"name"`
	Role                 UserRole   `json:"role"`
	Status               UserStatus `json:"status"`
	ProfilePhotoKey      *string    `json:"profile_photo_key,omitempty"`
	GoogleID             *string    `json:"google_id,omitempty"`
	FailedLoginAttempts  int        `json:"-"`
	LockedUntil          *time.Time `json:"-"`
	LastLoginAt          *time.Time `json:"last_login_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// IsLocked returns true if the account is temporarily locked.
func (u *User) IsLocked() bool {
	return u.LockedUntil != nil && time.Now().Before(*u.LockedUntil)
}

// ─── Session / tokens ─────────────────────────────────────────────────────────

// Claims is embedded in a JWT access token.
type Claims struct {
	UserID   uuid.UUID `json:"uid"`
	TenantID uuid.UUID `json:"tid"`
	Email    string    `json:"email"`
	Role     UserRole  `json:"role"`
	Name     string    `json:"name"`
}

// RefreshToken represents a persisted opaque refresh token.
type RefreshToken struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

// EmailVerification token record.
type EmailVerification struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// PasswordResetToken record.
type PasswordResetToken struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ─── API Key ──────────────────────────────────────────────────────────────────

type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	UserID     uuid.UUID  `json:"user_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyHash    string     `json:"-"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Revoked    bool       `json:"revoked"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ─── Request / Response DTOs ──────────────────────────────────────────────────

type RegisterRequest struct {
	TenantSlug string `json:"tenant_slug"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type TokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	User         User      `json:"user"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type VerifyEmailRequest struct {
	Token string `json:"token"`
}

type UpdateProfileRequest struct {
	Name string `json:"name"`
}

// GoogleLoginRequest is used internally for Google OAuth callback processing.
type GoogleUserInfo struct {
	GoogleID    string `json:"sub"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	VerifiedEmail bool `json:"verified_email"`
	PictureURL  string `json:"picture"`
}

type CreateTenantRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type UpdateUserRoleRequest struct {
	Role UserRole `json:"role"`
}

type CreateAPIKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreateAPIKeyResponse struct {
	APIKey    APIKey `json:"api_key"`
	RawKey    string `json:"raw_key"` // shown once only
}

type ListUsersResponse struct {
	Users    []User `json:"users"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services"`
}

type MessageResponse struct {
	Message string `json:"message"`
}
