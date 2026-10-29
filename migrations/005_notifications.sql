-- ============================================================
-- EthioScript — Notification Service Tables
-- Migration: 005_notifications.sql
-- ============================================================

-- ─── ENUMS ──────────────────────────────────────────────────────────────────

CREATE TYPE notification_type AS ENUM (
    'JOB_COMPLETED',
    'JOB_FAILED',
    'DOCUMENT_READY',
    'DOCUMENT_FAILED',
    'SYSTEM_ALERT',
    'OTP',
    'WELCOME',
    'PASSWORD_RESET',
    'EMAIL_VERIFIED',
    'ADMIN_MESSAGE'
);

CREATE TYPE notification_channel AS ENUM (
    'IN_APP',
    'EMAIL',
    'SMS',
    'PUSH',
    'TELEGRAM'
);

CREATE TYPE notification_status AS ENUM (
    'PENDING',
    'SENT',
    'DELIVERED',
    'FAILED',
    'READ'
);

CREATE TYPE otp_purpose AS ENUM (
    'EMAIL_VERIFICATION',
    'PASSWORD_RESET',
    'TWO_FACTOR_AUTH',
    'PHONE_VERIFICATION'
);

-- ─── NOTIFICATIONS (inbox) ───────────────────────────────────────────────────

CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID        NOT NULL,
    type            notification_type   NOT NULL,
    channel         notification_channel NOT NULL DEFAULT 'IN_APP',
    status          notification_status  NOT NULL DEFAULT 'PENDING',
    title           TEXT        NOT NULL,
    body            TEXT        NOT NULL,
    action_url      TEXT,                                   -- deep-link for frontend
    metadata        JSONB       NOT NULL DEFAULT '{}',     -- arbitrary extra data
    read_at         TIMESTAMPTZ,
    delivered_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_id       ON notifications (user_id);
CREATE INDEX idx_notifications_user_unread   ON notifications (user_id, read_at) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_created_at    ON notifications (created_at DESC);
CREATE INDEX idx_notifications_type          ON notifications (type);

-- ─── PUSH SUBSCRIPTIONS (Web Push / VAPID) ───────────────────────────────────

CREATE TABLE push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID        NOT NULL,
    endpoint        TEXT        NOT NULL UNIQUE,
    p256dh_key      TEXT        NOT NULL,   -- base64url public key
    auth_key        TEXT        NOT NULL,   -- base64url auth secret
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ
);

CREATE INDEX idx_push_subscriptions_user_id ON push_subscriptions (user_id);

-- ─── OTP CODES ───────────────────────────────────────────────────────────────

CREATE TABLE otp_codes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID,                               -- NULL for anonymous / phone OTP
    target          TEXT        NOT NULL,               -- email or phone number
    purpose         otp_purpose NOT NULL,
    code_hash       TEXT        NOT NULL,               -- bcrypt hash of the code
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    attempts        SMALLINT    NOT NULL DEFAULT 0,
    max_attempts    SMALLINT    NOT NULL DEFAULT 5,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_otp_codes_target   ON otp_codes (target, purpose);
CREATE INDEX idx_otp_codes_user_id  ON otp_codes (user_id) WHERE user_id IS NOT NULL;

-- ─── NOTIFICATION OUTBOX (transactional outbox pattern) ──────────────────────
-- Internal workers pick up rows from here and dispatch to channels

CREATE TABLE notification_outbox (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    notification_id UUID        REFERENCES notifications(id) ON DELETE CASCADE,
    channel         notification_channel NOT NULL,
    payload         JSONB       NOT NULL DEFAULT '{}',
    attempts        SMALLINT    NOT NULL DEFAULT 0,
    max_attempts    SMALLINT    NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    processed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outbox_pending ON notification_outbox (next_attempt_at)
    WHERE processed_at IS NULL;

-- ─── PUSH DELIVERY ATTEMPTS ──────────────────────────────────────────────────

CREATE TABLE push_delivery_attempts (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    notification_id     UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    subscription_id     UUID NOT NULL REFERENCES push_subscriptions(id) ON DELETE CASCADE,
    status              TEXT NOT NULL,      -- 'sent' | 'failed' | 'gone'
    status_code         INT,
    error_detail        TEXT,
    attempted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_push_delivery_notification ON push_delivery_attempts (notification_id);

-- ─── updated_at trigger ──────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_notifications_updated_at
    BEFORE UPDATE ON notifications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
