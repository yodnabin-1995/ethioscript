-- ============================================================
-- EthioScript & Voice AI — Database Schema
-- Migration: 001_init_schema.sql
-- Covers all 9 entities from SRS Section 8
-- ============================================================

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";   -- for full-text search trigrams

-- ─── ENUMS ──────────────────────────────────────────────────────────────────

CREATE TYPE user_role AS ENUM (
    'TENANT_ADMIN',
    'CONTRIBUTOR',
    'REVIEWER',
    'READ_ONLY_AUDITOR',
    'PLATFORM_OPERATOR'
);

CREATE TYPE user_status AS ENUM (
    'ACTIVE',
    'SUSPENDED',
    'PENDING_VERIFICATION',
    'DELETED'
);

CREATE TYPE document_status AS ENUM (
    'UPLOADED',
    'QUEUED',
    'PROCESSING',
    'REVIEW_REQUIRED',
    'APPROVED',
    'FAILED',
    'CANCELLED',
    'ARCHIVED',
    'DELETED'
);

CREATE TYPE job_state AS ENUM (
    'PENDING',
    'QUEUED',
    'PROCESSING',
    'REVIEW_REQUIRED',
    'COMPLETED',
    'FAILED',
    'CANCELLED',
    'RETRYING'
);

CREATE TYPE task_type AS ENUM (
    'OCR',
    'STT',
    'CLASSIFICATION',
    'EXPORT'
);

CREATE TYPE export_format AS ENUM (
    'TXT',
    'DOCX',
    'PDF_SEARCHABLE',
    'JSON'
);

CREATE TYPE audit_action AS ENUM (
    'USER_CREATED',
    'USER_UPDATED',
    'USER_DELETED',
    'USER_LOGIN',
    'USER_LOGOUT',
    'USER_TOKEN_REFRESHED',
    'DOCUMENT_UPLOADED',
    'DOCUMENT_PROCESSED',
    'DOCUMENT_APPROVED',
    'DOCUMENT_REJECTED',
    'DOCUMENT_DELETED',
    'DOCUMENT_EXPORTED',
    'JOB_CREATED',
    'JOB_STARTED',
    'JOB_COMPLETED',
    'JOB_FAILED',
    'JOB_RETRIED',
    'JOB_CANCELLED',
    'JOB_DEAD_LETTERED',
    'RESULT_DRAFT_SAVED',
    'RESULT_APPROVED',
    'RESULT_CORRECTION_APPLIED',
    'TENANT_CREATED',
    'TENANT_UPDATED',
    'PERMISSION_CHANGED',
    'RETENTION_POLICY_APPLIED',
    'ASSET_DELETED',
    'API_KEY_CREATED',
    'API_KEY_REVOKED'
);

CREATE TYPE unit_type AS ENUM (
    'PAGE',
    'AUDIO_MINUTE',
    'BYTES_STORED',
    'INFERENCE_UNIT',
    'EXPORT_OPERATION'
);

-- ─── 1. TENANTS ──────────────────────────────────────────────────────────────

CREATE TABLE tenants (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name                VARCHAR(255)    NOT NULL,
    slug                VARCHAR(100)    NOT NULL UNIQUE,
    policy              JSONB           NOT NULL DEFAULT '{}',
    max_upload_size_mb  INTEGER         NOT NULL DEFAULT 50,
    max_storage_gb      INTEGER         NOT NULL DEFAULT 10,
    retention_days      INTEGER         NOT NULL DEFAULT 365,
    is_active           BOOLEAN         NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants (slug);
CREATE INDEX idx_tenants_is_active ON tenants (is_active);

-- ─── 2. USERS ────────────────────────────────────────────────────────────────

CREATE TABLE users (
    id                      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id               UUID            NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    email                   VARCHAR(320)    NOT NULL,
    email_verified          BOOLEAN         NOT NULL DEFAULT FALSE,
    password_hash           TEXT            NOT NULL,
    name                    VARCHAR(255)    NOT NULL,
    role                    user_role       NOT NULL DEFAULT 'CONTRIBUTOR',
    status                  user_status     NOT NULL DEFAULT 'PENDING_VERIFICATION',
    last_login_at           TIMESTAMPTZ,
    failed_login_attempts   INTEGER         NOT NULL DEFAULT 0,
    locked_until            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, email)
);

CREATE INDEX idx_users_tenant_id     ON users (tenant_id);
CREATE INDEX idx_users_email         ON users (email);
CREATE INDEX idx_users_role          ON users (role);
CREATE INDEX idx_users_status        ON users (status);

-- Refresh tokens (Better Auth style)
CREATE TABLE refresh_tokens (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tenant_id   UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user_id    ON refresh_tokens (user_id);
CREATE INDEX idx_refresh_tokens_token_hash ON refresh_tokens (token_hash);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);

-- API Keys
CREATE TABLE api_keys (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id   UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    key_hash    TEXT        NOT NULL UNIQUE,
    key_prefix  CHAR(8)     NOT NULL,
    scopes      TEXT[]      NOT NULL DEFAULT '{}',
    expires_at  TIMESTAMPTZ,
    revoked     BOOLEAN     NOT NULL DEFAULT FALSE,
    last_used_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_keys_tenant_id  ON api_keys (tenant_id);
CREATE INDEX idx_api_keys_key_hash   ON api_keys (key_hash);
CREATE INDEX idx_api_keys_revoked    ON api_keys (revoked);

-- ─── 3. ASSETS ───────────────────────────────────────────────────────────────

CREATE TABLE assets (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id           UUID        NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    object_key          TEXT        NOT NULL UNIQUE,
    original_filename   TEXT        NOT NULL,
    checksum_sha256     CHAR(64)    NOT NULL,
    media_type          VARCHAR(100) NOT NULL,
    size_bytes          BIGINT      NOT NULL,
    scan_status         VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    scan_result         VARCHAR(50),
    bucket              VARCHAR(100) NOT NULL,
    retention_until     TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ,
    created_by          UUID        NOT NULL REFERENCES users (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_assets_tenant_id       ON assets (tenant_id);
CREATE INDEX idx_assets_object_key      ON assets (object_key);
CREATE INDEX idx_assets_created_by      ON assets (created_by);
CREATE INDEX idx_assets_media_type      ON assets (media_type);
CREATE INDEX idx_assets_deleted_at      ON assets (deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_assets_checksum        ON assets (checksum_sha256);

-- ─── 4. DOCUMENTS ────────────────────────────────────────────────────────────

CREATE TABLE documents (
    id              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID            NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    asset_id        UUID            NOT NULL REFERENCES assets (id) ON DELETE RESTRICT,
    filename        TEXT            NOT NULL,
    language        VARCHAR(20)     NOT NULL DEFAULT 'amh',
    task_type       task_type       NOT NULL DEFAULT 'OCR',
    status          document_status NOT NULL DEFAULT 'UPLOADED',
    title           VARCHAR(500),
    description     TEXT,
    tags            TEXT[]          NOT NULL DEFAULT '{}',
    metadata        JSONB           NOT NULL DEFAULT '{}',
    page_count      INTEGER,
    duration_seconds FLOAT,
    created_by      UUID            NOT NULL REFERENCES users (id),
    assigned_to     UUID            REFERENCES users (id),
    approved_by     UUID            REFERENCES users (id),
    approved_at     TIMESTAMPTZ,
    retention_until TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_documents_tenant_id    ON documents (tenant_id);
CREATE INDEX idx_documents_asset_id     ON documents (asset_id);
CREATE INDEX idx_documents_status       ON documents (status);
CREATE INDEX idx_documents_language     ON documents (language);
CREATE INDEX idx_documents_task_type    ON documents (task_type);
CREATE INDEX idx_documents_created_by   ON documents (created_by);
CREATE INDEX idx_documents_assigned_to  ON documents (assigned_to);
CREATE INDEX idx_documents_created_at   ON documents (created_at DESC);
CREATE INDEX idx_documents_deleted_at   ON documents (deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_documents_tags         ON documents USING gin (tags);
CREATE INDEX idx_documents_metadata     ON documents USING gin (metadata);

-- ─── 5. PROCESSING JOBS ──────────────────────────────────────────────────────

CREATE TABLE processing_jobs (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    document_id         UUID        NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    tenant_id           UUID        NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    task_type           task_type   NOT NULL,
    state               job_state   NOT NULL DEFAULT 'PENDING',
    attempts            INTEGER     NOT NULL DEFAULT 0,
    max_attempts        INTEGER     NOT NULL DEFAULT 3,
    worker_id           TEXT,
    lease_expires_at    TIMESTAMPTZ,
    last_heartbeat_at   TIMESTAMPTZ,
    model_name          VARCHAR(100),
    model_version       VARCHAR(50),
    model_config        JSONB       NOT NULL DEFAULT '{}',
    language            VARCHAR(20),
    error_code          VARCHAR(100),
    error_message       TEXT,
    retry_after         TIMESTAMPTZ,
    priority            INTEGER     NOT NULL DEFAULT 5,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_jobs_document_id       ON processing_jobs (document_id);
CREATE INDEX idx_jobs_tenant_id         ON processing_jobs (tenant_id);
CREATE INDEX idx_jobs_state             ON processing_jobs (state);
CREATE INDEX idx_jobs_task_type         ON processing_jobs (task_type);
CREATE INDEX idx_jobs_worker_id         ON processing_jobs (worker_id) WHERE worker_id IS NOT NULL;
CREATE INDEX idx_jobs_lease_expires_at  ON processing_jobs (lease_expires_at) WHERE state = 'PROCESSING';
CREATE INDEX idx_jobs_retry_after       ON processing_jobs (retry_after) WHERE state = 'RETRYING';
CREATE INDEX idx_jobs_created_at        ON processing_jobs (created_at DESC);
CREATE INDEX idx_jobs_pending_queued    ON processing_jobs (priority DESC, created_at ASC)
    WHERE state IN ('PENDING', 'QUEUED');

-- ─── 6. OUTBOX EVENTS ────────────────────────────────────────────────────────

CREATE TABLE outbox_events (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    aggregate_id    UUID        NOT NULL,
    aggregate_type  VARCHAR(50) NOT NULL,
    event_type      VARCHAR(100) NOT NULL,
    schema_version  INTEGER     NOT NULL DEFAULT 1,
    tenant_id       UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    correlation_id  UUID,
    causation_id    UUID,
    payload         JSONB       NOT NULL,
    published       BOOLEAN     NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    publish_attempts INTEGER    NOT NULL DEFAULT 0,
    error           TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outbox_published       ON outbox_events (published, created_at ASC)
    WHERE published = FALSE;
CREATE INDEX idx_outbox_aggregate_id    ON outbox_events (aggregate_id);
CREATE INDEX idx_outbox_event_type      ON outbox_events (event_type);
CREATE INDEX idx_outbox_tenant_id       ON outbox_events (tenant_id);
CREATE INDEX idx_outbox_correlation_id  ON outbox_events (correlation_id) WHERE correlation_id IS NOT NULL;

-- ─── 7. RESULT VERSIONS ──────────────────────────────────────────────────────

CREATE TABLE result_versions (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    document_id         UUID        NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    tenant_id           UUID        NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    job_id              UUID        REFERENCES processing_jobs (id),
    version             INTEGER     NOT NULL,
    is_approved         BOOLEAN     NOT NULL DEFAULT FALSE,
    raw_text_key        TEXT,
    approved_text_key   TEXT,
    raw_text_inline     TEXT,
    approved_text       TEXT,
    confidence_score    FLOAT,
    confidence_signals  JSONB       NOT NULL DEFAULT '{}',
    model_metadata      JSONB       NOT NULL DEFAULT '{}',
    page_count          INTEGER,
    word_count          INTEGER,
    char_count          INTEGER,
    segments            JSONB,
    layout_artifacts    JSONB,
    created_by          UUID        NOT NULL REFERENCES users (id),
    approved_by         UUID        REFERENCES users (id),
    approved_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_id, version)
);

CREATE INDEX idx_result_versions_document_id    ON result_versions (document_id);
CREATE INDEX idx_result_versions_tenant_id      ON result_versions (tenant_id);
CREATE INDEX idx_result_versions_job_id         ON result_versions (job_id);
CREATE INDEX idx_result_versions_is_approved    ON result_versions (is_approved);
CREATE INDEX idx_result_versions_version        ON result_versions (document_id, version DESC);

-- Full-text search on approved text
CREATE INDEX idx_result_versions_fts ON result_versions
    USING gin (to_tsvector('simple', COALESCE(approved_text, '')));

-- ─── 8. AUDIT EVENTS ─────────────────────────────────────────────────────────

CREATE TABLE audit_events (
    id              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID            NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    actor_id        UUID            REFERENCES users (id) ON DELETE SET NULL,
    actor_email     VARCHAR(320),
    actor_role      user_role,
    action          audit_action    NOT NULL,
    resource_type   VARCHAR(50)     NOT NULL,
    resource_id     UUID,
    ip_address      INET,
    user_agent      TEXT,
    correlation_id  UUID,
    before_state    JSONB,
    after_state     JSONB,
    metadata        JSONB           NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_tenant_id        ON audit_events (tenant_id);
CREATE INDEX idx_audit_actor_id         ON audit_events (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX idx_audit_action           ON audit_events (action);
CREATE INDEX idx_audit_resource_type    ON audit_events (resource_type);
CREATE INDEX idx_audit_resource_id      ON audit_events (resource_id) WHERE resource_id IS NOT NULL;
CREATE INDEX idx_audit_occurred_at      ON audit_events (occurred_at DESC);
CREATE INDEX idx_audit_correlation_id   ON audit_events (correlation_id) WHERE correlation_id IS NOT NULL;

-- ─── 9. USAGE RECORDS ────────────────────────────────────────────────────────

CREATE TABLE usage_records (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID        NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    user_id         UUID        REFERENCES users (id) ON DELETE SET NULL,
    job_id          UUID        REFERENCES processing_jobs (id) ON DELETE SET NULL,
    document_id     UUID        REFERENCES documents (id) ON DELETE SET NULL,
    unit_type       unit_type   NOT NULL,
    quantity        NUMERIC(12,4) NOT NULL,
    unit_cost       NUMERIC(12,6) NOT NULL DEFAULT 0,
    total_cost      NUMERIC(12,6) GENERATED ALWAYS AS (quantity * unit_cost) STORED,
    period_month    CHAR(7)     NOT NULL, -- YYYY-MM
    metadata        JSONB       NOT NULL DEFAULT '{}',
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_tenant_id        ON usage_records (tenant_id);
CREATE INDEX idx_usage_user_id          ON usage_records (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_usage_job_id           ON usage_records (job_id) WHERE job_id IS NOT NULL;
CREATE INDEX idx_usage_unit_type        ON usage_records (unit_type);
CREATE INDEX idx_usage_period_month     ON usage_records (tenant_id, period_month);
CREATE INDEX idx_usage_recorded_at      ON usage_records (recorded_at DESC);

-- ─── EXPORT ARTIFACTS ────────────────────────────────────────────────────────

CREATE TABLE export_artifacts (
    id              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID            NOT NULL REFERENCES tenants (id) ON DELETE RESTRICT,
    document_id     UUID            NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    result_version  INTEGER         NOT NULL,
    format          export_format   NOT NULL,
    object_key      TEXT            NOT NULL,
    size_bytes      BIGINT,
    status          VARCHAR(50)     NOT NULL DEFAULT 'PENDING',
    error           TEXT,
    expires_at      TIMESTAMPTZ,
    created_by      UUID            NOT NULL REFERENCES users (id),
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_exports_tenant_id      ON export_artifacts (tenant_id);
CREATE INDEX idx_exports_document_id    ON export_artifacts (document_id);
CREATE INDEX idx_exports_created_by     ON export_artifacts (created_by);
CREATE INDEX idx_exports_created_at     ON export_artifacts (created_at DESC);

-- ─── TRIGGERS: updated_at auto-update ────────────────────────────────────────

CREATE OR REPLACE FUNCTION trigger_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER set_updated_at_tenants
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

CREATE TRIGGER set_updated_at_users
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

CREATE TRIGGER set_updated_at_documents
    BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

CREATE TRIGGER set_updated_at_processing_jobs
    BEFORE UPDATE ON processing_jobs
    FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

CREATE TRIGGER set_updated_at_result_versions
    BEFORE UPDATE ON result_versions
    FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

-- ─── SEED: default tenant ────────────────────────────────────────────────────

INSERT INTO tenants (id, name, slug, policy)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'Default Tenant',
    'default',
    '{"max_upload_size_mb": 50, "allowed_languages": ["amh", "eng", "tir", "orm"]}'
);
