-- ============================================================
-- Migration 003: User profile extensions
-- Adds profile photo storage key and Google OAuth ID columns
-- ============================================================

-- Profile photo object key in MinIO (nullable — not all users have a photo)
ALTER TABLE users ADD COLUMN IF NOT EXISTS profile_photo_key TEXT;

-- Google OAuth subject ID (nullable — only set for Google-authenticated users)
ALTER TABLE users ADD COLUMN IF NOT EXISTS google_id VARCHAR(255);

-- Make password_hash nullable for Google-only users who never set a password
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- Unique index on google_id (partial — only for non-null values)
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_google_id
    ON users (google_id)
    WHERE google_id IS NOT NULL;
