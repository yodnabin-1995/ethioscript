-- ============================================================
-- Migration 004: Simplify roles to USER and ADMIN
-- Replaces: CONTRIBUTOR, TENANT_ADMIN, REVIEWER,
--           READ_ONLY_AUDITOR, PLATFORM_OPERATOR
-- ============================================================

-- 1. Add new enum values
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'USER';
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'ADMIN';

-- 2. Migrate existing users to new roles
UPDATE users SET role = 'USER'  WHERE role IN ('CONTRIBUTOR', 'REVIEWER', 'READ_ONLY_AUDITOR');
UPDATE users SET role = 'ADMIN' WHERE role IN ('TENANT_ADMIN', 'PLATFORM_OPERATOR');

-- 3. Update default on the column
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'USER';
