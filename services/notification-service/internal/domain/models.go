// Package domain holds all notification-service core entities and value objects.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ─── Enums ────────────────────────────────────────────────────────────────────

type NotificationType string

const (
	TypeJobCompleted   NotificationType = "JOB_COMPLETED"
	TypeJobFailed      NotificationType = "JOB_FAILED"
	TypeDocumentReady  NotificationType = "DOCUMENT_READY"
	TypeDocumentFailed NotificationType = "DOCUMENT_FAILED"
	TypeSystemAlert    NotificationType = "SYSTEM_ALERT"
	TypeOTP            NotificationType = "OTP"
	TypeWelcome        NotificationType = "WELCOME"
	TypePasswordReset  NotificationType = "PASSWORD_RESET"
	TypeEmailVerified  NotificationType = "EMAIL_VERIFIED"
	TypeAdminMessage   NotificationType = "ADMIN_MESSAGE"
)

type NotificationChannel string

const (
	ChannelInApp    NotificationChannel = "IN_APP"
	ChannelEmail    NotificationChannel = "EMAIL"
	ChannelSMS      NotificationChannel = "SMS"
	ChannelPush     NotificationChannel = "PUSH"
	ChannelTelegram NotificationChannel = "TELEGRAM"
)

type NotificationStatus string

const (
	StatusPending   NotificationStatus = "PENDING"
	StatusSent      NotificationStatus = "SENT"
	StatusDelivered NotificationStatus = "DELIVERED"
	StatusFailed    NotificationStatus = "FAILED"
	StatusRead      NotificationStatus = "READ"
)

type OTPPurpose string

const (
	OTPEmailVerification OTPPurpose = "EMAIL_VERIFICATION"
	OTPPasswordReset     OTPPurpose = "PASSWORD_RESET"
	OTPTwoFactor         OTPPurpose = "TWO_FACTOR_AUTH"
	OTPPhoneVerification OTPPurpose = "PHONE_VERIFICATION"
)

// ─── Domain errors ────────────────────────────────────────────────────────────

var (
	ErrNotFound    = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden   = errors.New("forbidden")
	ErrConflict    = errors.New("conflict")
	ErrValidation  = errors.New("validation error")
	ErrOTPExpired  = errors.New("otp expired")
	ErrOTPInvalid  = errors.New("otp invalid")
	ErrOTPLocked   = errors.New("too many otp attempts")
	ErrRateLimited = errors.New("rate limit exceeded")
)

// ─── Notification ─────────────────────────────────────────────────────────────

type Notification struct {
	ID          uuid.UUID           `json:"id"`
	UserID      uuid.UUID           `json:"user_id"`
	Type        NotificationType    `json:"type"`
	Channel     NotificationChannel `json:"channel"`
	Status      NotificationStatus  `json:"status"`
	Title       string              `json:"title"`
	Body        string              `json:"body"`
	ActionURL   *string             `json:"action_url,omitempty"`
	Metadata    map[string]any      `json:"metadata"`
	ReadAt      *time.Time          `json:"read_at,omitempty"`
	DeliveredAt *time.Time          `json:"delivered_at,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// ─── Push Subscription ────────────────────────────────────────────────────────

type PushSubscription struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Endpoint   string     `json:"endpoint"`
	P256DHKey  string     `json:"p256dh_key"`
	AuthKey    string     `json:"auth_key"`
	UserAgent  *string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// ─── OTP ─────────────────────────────────────────────────────────────────────

type OTPCode struct {
	ID          uuid.UUID  `json:"id"`
	UserID      *uuid.UUID `json:"user_id,omitempty"`
	Target      string     `json:"target"`
	Purpose     OTPPurpose `json:"purpose"`
	CodeHash    string     `json:"-"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ─── Outbox ───────────────────────────────────────────────────────────────────

type OutboxEntry struct {
	ID             uuid.UUID           `json:"id"`
	NotificationID uuid.UUID           `json:"notification_id"`
	Channel        NotificationChannel `json:"channel"`
	Payload        map[string]any      `json:"payload"`
	Attempts       int                 `json:"attempts"`
	MaxAttempts    int                 `json:"max_attempts"`
	NextAttemptAt  time.Time           `json:"next_attempt_at"`
	LastError      *string             `json:"last_error,omitempty"`
	ProcessedAt    *time.Time          `json:"processed_at,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
}

// ─── Request / Response DTOs ──────────────────────────────────────────────────

type InboxResponse struct {
	Notifications []Notification `json:"notifications"`
	Total         int            `json:"total"`
	Page          int            `json:"page"`
	PageSize      int            `json:"page_size"`
	UnreadCount   int            `json:"unread_count"`
}

type UnreadCountResponse struct {
	UnreadCount int `json:"unread_count"`
}

type MarkReadResponse struct {
	ID     uuid.UUID `json:"id"`
	ReadAt time.Time `json:"read_at"`
}

type MarkAllReadResponse struct {
	MarkedCount int `json:"marked_count"`
}

type PushSubscribeRequest struct {
	Endpoint  string  `json:"endpoint"`
	P256DHKey string  `json:"p256dh_key"`
	AuthKey   string  `json:"auth_key"`
	UserAgent *string `json:"user_agent,omitempty"`
}

type PushSubscribeResponse struct {
	ID      uuid.UUID `json:"id"`
	Message string    `json:"message"`
}

type VapidPublicKeyResponse struct {
	VapidPublicKey       string `json:"vapid_public_key"`
	ApplicationServerKey string `json:"application_server_key"`
}

type OTPSendRequest struct {
	Target  string     `json:"target"`
	Purpose OTPPurpose `json:"purpose"`
	UserID  *string    `json:"user_id,omitempty"`
}

type OTPSendResponse struct {
	Message       string    `json:"message"`
	ExpiresAt     time.Time `json:"expires_at"`
	MaskedTarget  string    `json:"masked_target"`
}

type OTPVerifyRequest struct {
	Target  string     `json:"target"`
	Purpose OTPPurpose `json:"purpose"`
	Code    string     `json:"code"`
}

type OTPVerifyResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

type EmailNotificationRequest struct {
	UserID     string         `json:"user_id"`
	ToEmail    string         `json:"to_email"`
	Subject    string         `json:"subject"`
	BodyHTML   string         `json:"body_html"`
	BodyText   string         `json:"body_text,omitempty"`
	TemplateID string         `json:"template_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type SMSNotificationRequest struct {
	UserID      string         `json:"user_id"`
	PhoneNumber string         `json:"phone_number"`
	Message     string         `json:"message"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type TelegramNotificationRequest struct {
	UserID    string         `json:"user_id"`
	ChatID    string         `json:"chat_id"`
	Message   string         `json:"message"`
	ParseMode string         `json:"parse_mode,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type BulkNotificationRequest struct {
	UserIDs   []string            `json:"user_ids"`
	Type      NotificationType    `json:"type"`
	Channel   NotificationChannel `json:"channel"`
	Title     string              `json:"title"`
	Body      string              `json:"body"`
	ActionURL *string             `json:"action_url,omitempty"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
}

type BulkNotificationResponse struct {
	QueuedCount int    `json:"queued_count"`
	Message     string `json:"message"`
}

type InternalEventRequest struct {
	EventType NotificationType    `json:"event_type"`
	UserID    string              `json:"user_id"`
	Title     string              `json:"title"`
	Body      string              `json:"body"`
	Channel   NotificationChannel `json:"channel"`
	ActionURL *string             `json:"action_url,omitempty"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
}

type InternalEventResponse struct {
	NotificationID uuid.UUID `json:"notification_id"`
	Message        string    `json:"message"`
}

type AdminStatsResponse struct {
	TotalNotifications int            `json:"total_notifications"`
	UnreadTotal        int            `json:"unread_total"`
	ByType             map[string]int `json:"by_type"`
	ByChannel          map[string]int `json:"by_channel"`
	ByStatus           map[string]int `json:"by_status"`
	PushSubscriptions  int            `json:"push_subscriptions"`
	OTPPending         int            `json:"otp_pending"`
	OutboxPending      int            `json:"outbox_pending"`
	GeneratedAt        time.Time      `json:"generated_at"`
}

type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

// ─── JWT Claims ───────────────────────────────────────────────────────────────

type UserRole string

const (
	RoleUser  UserRole = "USER"
	RoleAdmin UserRole = "ADMIN"
)

type Claims struct {
	UserID   uuid.UUID `json:"uid"`
	TenantID uuid.UUID `json:"tid"`
	Email    string    `json:"email"`
	Role     UserRole  `json:"role"`
	Name     string    `json:"name"`
}
