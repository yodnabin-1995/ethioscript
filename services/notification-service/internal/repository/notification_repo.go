// Package repository handles all PostgreSQL queries for the notification-service.
package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationRepo is the single DB access object for this service.
type NotificationRepo struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{db: db}
}

// ─── Notifications ────────────────────────────────────────────────────────────

// Create inserts a notification and queues it in the outbox — both in one transaction.
func (r *NotificationRepo) Create(
	ctx context.Context,
	userID uuid.UUID,
	nType domain.NotificationType,
	channel domain.NotificationChannel,
	title, body string,
	actionURL *string,
	metadata map[string]any,
) (*domain.Notification, error) {
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}
	payloadJSON, _ := json.Marshal(map[string]any{
		"user_id":    userID.String(),
		"title":      title,
		"body":       body,
		"action_url": actionURL,
		"metadata":   metadata,
	})

	id := uuid.New()

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var n domain.Notification
	err = tx.QueryRow(ctx, `
		INSERT INTO notifications
		    (id, user_id, type, channel, status, title, body, action_url, metadata)
		VALUES ($1,$2,$3::notification_type,$4::notification_channel,
		        'PENDING'::notification_status,$5,$6,$7,$8::jsonb)
		RETURNING id,user_id,type,channel,status,title,body,action_url,
		          metadata,read_at,delivered_at,created_at,updated_at`,
		id, userID, string(nType), string(channel),
		title, body, actionURL, string(metaJSON),
	).Scan(
		&n.ID, &n.UserID, &n.Type, &n.Channel, &n.Status,
		&n.Title, &n.Body, &n.ActionURL, (*jsonRaw)(&n.Metadata),
		&n.ReadAt, &n.DeliveredAt, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO notification_outbox (notification_id, channel, payload)
		VALUES ($1, $2::notification_channel, $3::jsonb)`,
		id, string(channel), string(payloadJSON),
	)
	if err != nil {
		return nil, err
	}

	return &n, tx.Commit(ctx)
}

// ListByUser returns paginated notifications for a user.
func (r *NotificationRepo) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
	page, pageSize int,
	unreadOnly bool,
) ([]domain.Notification, int, int, error) {
	offset := (page - 1) * pageSize

	whereExtra := ""
	if unreadOnly {
		whereExtra = " AND read_at IS NULL"
	}

	rows, err := r.db.Query(ctx, `
		SELECT id,user_id,type,channel,status,title,body,action_url,
		       metadata,read_at,delivered_at,created_at,updated_at
		FROM notifications
		WHERE user_id=$1`+whereExtra+`
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		userID, pageSize, offset,
	)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var list []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.Type, &n.Channel, &n.Status,
			&n.Title, &n.Body, &n.ActionURL, (*jsonRaw)(&n.Metadata),
			&n.ReadAt, &n.DeliveredAt, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, 0, 0, err
		}
		list = append(list, n)
	}

	var total int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id=$1`+whereExtra,
		userID,
	).Scan(&total)

	var unread int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL`,
		userID,
	).Scan(&unread)

	return list, total, unread, nil
}

// UnreadCount returns the unread badge count for a user.
func (r *NotificationRepo) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL`,
		userID,
	).Scan(&count)
	return count, err
}

// MarkRead marks a single notification as read. Returns the updated read_at or
// domain.ErrNotFound if the notification doesn't belong to the user.
func (r *NotificationRepo) MarkRead(ctx context.Context, id, userID uuid.UUID) (time.Time, error) {
	var readAt time.Time
	err := r.db.QueryRow(ctx, `
		UPDATE notifications
		SET read_at=NOW(), status='READ'::notification_status, updated_at=NOW()
		WHERE id=$1 AND user_id=$2
		RETURNING COALESCE(read_at, NOW())`,
		id, userID,
	).Scan(&readAt)
	if err == pgx.ErrNoRows {
		return time.Time{}, domain.ErrNotFound
	}
	return readAt, err
}

// MarkAllRead marks every unread notification for a user as read.
func (r *NotificationRepo) MarkAllRead(ctx context.Context, userID uuid.UUID) (int, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications
		SET read_at=NOW(), status='READ'::notification_status, updated_at=NOW()
		WHERE user_id=$1 AND read_at IS NULL`,
		userID,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// MarkDelivered sets a notification to DELIVERED status.
func (r *NotificationRepo) MarkDelivered(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE notifications
		SET status='DELIVERED'::notification_status, delivered_at=NOW(), updated_at=NOW()
		WHERE id=$1`,
		id,
	)
	return err
}

// ─── Push Subscriptions ───────────────────────────────────────────────────────

func (r *NotificationRepo) UpsertPushSubscription(
	ctx context.Context,
	userID uuid.UUID,
	endpoint, p256dh, authKey string,
	userAgent *string,
) (uuid.UUID, error) {
	id := uuid.New()
	err := r.db.QueryRow(ctx, `
		INSERT INTO push_subscriptions (id,user_id,endpoint,p256dh_key,auth_key,user_agent)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (endpoint) DO UPDATE
		    SET user_id=EXCLUDED.user_id,
		        p256dh_key=EXCLUDED.p256dh_key,
		        auth_key=EXCLUDED.auth_key,
		        last_used_at=NOW()
		RETURNING id`,
		id, userID, endpoint, p256dh, authKey, userAgent,
	).Scan(&id)
	return id, err
}

func (r *NotificationRepo) DeletePushSubscription(ctx context.Context, userID uuid.UUID, endpoint string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE user_id=$1 AND endpoint=$2`,
		userID, endpoint,
	)
	return err
}

func (r *NotificationRepo) GetPushSubscriptionsByUser(ctx context.Context, userID uuid.UUID) ([]domain.PushSubscription, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id,user_id,endpoint,p256dh_key,auth_key,user_agent,created_at,last_used_at
		FROM push_subscriptions WHERE user_id=$1`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.PushSubscription
	for rows.Next() {
		var s domain.PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256DHKey, &s.AuthKey,
			&s.UserAgent, &s.CreatedAt, &s.LastUsedAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

func (r *NotificationRepo) CountPushSubscriptions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM push_subscriptions`).Scan(&count)
	return count, err
}

// ─── OTP ─────────────────────────────────────────────────────────────────────

func (r *NotificationRepo) CreateOTP(
	ctx context.Context,
	userID *uuid.UUID,
	target string,
	purpose domain.OTPPurpose,
	codeHash string,
	expiresAt time.Time,
) error {
	// Invalidate any existing unused OTPs for this target+purpose
	_, _ = r.db.Exec(ctx, `
		UPDATE otp_codes SET used_at=NOW()
		WHERE target=$1 AND purpose=$2::otp_purpose AND used_at IS NULL`,
		target, string(purpose),
	)

	_, err := r.db.Exec(ctx, `
		INSERT INTO otp_codes (id,user_id,target,purpose,code_hash,expires_at)
		VALUES ($1,$2,$3,$4::otp_purpose,$5,$6)`,
		uuid.New(), userID, target, string(purpose), codeHash, expiresAt,
	)
	return err
}

// GetActiveOTP returns the latest unused, unexpired OTP for target+purpose.
func (r *NotificationRepo) GetActiveOTP(ctx context.Context, target string, purpose domain.OTPPurpose) (*domain.OTPCode, error) {
	var o domain.OTPCode
	err := r.db.QueryRow(ctx, `
		SELECT id,user_id,target,purpose,code_hash,expires_at,used_at,attempts,max_attempts,created_at
		FROM otp_codes
		WHERE target=$1 AND purpose=$2::otp_purpose AND used_at IS NULL
		ORDER BY created_at DESC LIMIT 1`,
		target, string(purpose),
	).Scan(&o.ID, &o.UserID, &o.Target, &o.Purpose, &o.CodeHash,
		&o.ExpiresAt, &o.UsedAt, &o.Attempts, &o.MaxAttempts, &o.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	return &o, err
}

func (r *NotificationRepo) MarkOTPUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE otp_codes SET used_at=NOW() WHERE id=$1`, id)
	return err
}

func (r *NotificationRepo) IncrementOTPAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE otp_codes SET attempts=attempts+1 WHERE id=$1`, id)
	return err
}

func (r *NotificationRepo) CountPendingOTPs(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM otp_codes WHERE used_at IS NULL AND expires_at>NOW()`,
	).Scan(&count)
	return count, err
}

// ─── Outbox ───────────────────────────────────────────────────────────────────

// PendingOutbox returns up to limit rows that are due for processing.
func (r *NotificationRepo) PendingOutbox(ctx context.Context, limit int) ([]domain.OutboxEntry, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id,notification_id,channel,payload,attempts,max_attempts,next_attempt_at,last_error,processed_at,created_at
		FROM notification_outbox
		WHERE processed_at IS NULL
		  AND next_attempt_at<=NOW()
		  AND attempts<max_attempts
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.OutboxEntry
	for rows.Next() {
		var e domain.OutboxEntry
		if err := rows.Scan(
			&e.ID, &e.NotificationID, &e.Channel, (*jsonRaw)(&e.Payload),
			&e.Attempts, &e.MaxAttempts, &e.NextAttemptAt, &e.LastError,
			&e.ProcessedAt, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

func (r *NotificationRepo) MarkOutboxProcessed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE notification_outbox SET processed_at=NOW() WHERE id=$1`, id)
	return err
}

func (r *NotificationRepo) RetryOutbox(ctx context.Context, id uuid.UUID, backoffSecs int, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE notification_outbox
		SET attempts=attempts+1,
		    next_attempt_at=NOW()+$1*INTERVAL '1 second',
		    last_error=$2
		WHERE id=$3`,
		backoffSecs, errMsg, id,
	)
	return err
}

func (r *NotificationRepo) CountPendingOutbox(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notification_outbox WHERE processed_at IS NULL`,
	).Scan(&count)
	return count, err
}

// ─── Admin stats ──────────────────────────────────────────────────────────────

func (r *NotificationRepo) TotalNotifications(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&count)
	return count, err
}

func (r *NotificationRepo) TotalUnread(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`).Scan(&count)
	return count, err
}

func (r *NotificationRepo) CountByColumn(ctx context.Context, column string) (map[string]int, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+column+`, COUNT(*) FROM notifications GROUP BY `+column,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, nil
}

// LookupUserIDByDocumentID finds the user who owns a document.
// Used by the RabbitMQ consumer to resolve user_id from document events.
func (r *NotificationRepo) LookupUserIDByDocumentID(ctx context.Context, documentID uuid.UUID) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT created_by FROM documents WHERE id=$1`,
		documentID,
	).Scan(&userID)
	if err == pgx.ErrNoRows {
		return uuid.Nil, domain.ErrNotFound
	}
	return userID, err
}

// ─── jsonRaw — scans JSONB columns into map[string]any ───────────────────────

type jsonRaw map[string]any

func (j *jsonRaw) Scan(src any) error {
	if src == nil {
		*j = map[string]any{}
		return nil
	}
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, j)
	case string:
		return json.Unmarshal([]byte(v), j)
	}
	return nil
}

// DB exposes the pool for the health handler.
func (r *NotificationRepo) DB() *pgxpool.Pool {
	return r.db
}
