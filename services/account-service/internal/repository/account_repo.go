package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AccountRepo is the single PostgreSQL repository for all account operations.
type AccountRepo struct {
	db *pgxpool.Pool
}

func NewAccountRepo(db *pgxpool.Pool) *AccountRepo {
	return &AccountRepo{db: db}
}

// ─── Tenants ──────────────────────────────────────────────────────────────────

func (r *AccountRepo) GetTenantBySlug(ctx context.Context, slug string) (*domain.Tenant, error) {
	var t domain.Tenant
	err := r.db.QueryRow(ctx,
		`SELECT id, name, slug, max_upload_size_mb, max_storage_gb, retention_days, is_active, created_at, updated_at
		 FROM tenants WHERE slug = $1`, slug,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.MaxUploadSizeMB, &t.MaxStorageGB, &t.RetentionDays, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *AccountRepo) GetTenantByID(ctx context.Context, id uuid.UUID) (*domain.Tenant, error) {
	var t domain.Tenant
	err := r.db.QueryRow(ctx,
		`SELECT id, name, slug, max_upload_size_mb, max_storage_gb, retention_days, is_active, created_at, updated_at
		 FROM tenants WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.MaxUploadSizeMB, &t.MaxStorageGB, &t.RetentionDays, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *AccountRepo) CreateTenant(ctx context.Context, t *domain.Tenant) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, TRUE, $4, $5)`,
		t.ID, t.Name, t.Slug, t.CreatedAt, t.UpdatedAt,
	)
	if isDuplicate(err) {
		return domain.ErrConflict
	}
	return err
}

func (r *AccountRepo) ListTenants(ctx context.Context) ([]domain.Tenant, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, slug, is_active, created_at, updated_at FROM tenants ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []domain.Tenant
	for rows.Next() {
		var t domain.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.IsActive, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, nil
}

// ─── Users ────────────────────────────────────────────────────────────────────

func (r *AccountRepo) CreateUser(ctx context.Context, u *domain.User, passwordHash string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, name, role, status, email_verified, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE, $8, $9)`,
		u.ID, u.TenantID, u.Email, passwordHash, u.Name, u.Role, u.Status, u.CreatedAt, u.UpdatedAt,
	)
	if isDuplicate(err) {
		return domain.ErrConflict
	}
	return err
}

func (r *AccountRepo) GetUserByEmail(ctx context.Context, email string) (*domain.User, string, error) {
	var u domain.User
	var passwordHash string
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, email, name, role, status, email_verified,
		        profile_photo_key, google_id,
		        failed_login_attempts, locked_until, last_login_at, created_at, updated_at, password_hash
		 FROM users WHERE email = $1 AND status != 'DELETED'`, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.EmailVerified, &u.ProfilePhotoKey, &u.GoogleID,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", domain.ErrNotFound
	}
	return &u, passwordHash, err
}

func (r *AccountRepo) GetUserByID(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, email, name, role, status, email_verified,
		        profile_photo_key, google_id,
		        failed_login_attempts, locked_until, last_login_at, created_at, updated_at
		 FROM users WHERE id = $1 AND status != 'DELETED'`, userID,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.EmailVerified, &u.ProfilePhotoKey, &u.GoogleID,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &u, err
}

func (r *AccountRepo) GetUserPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash string
	err := r.db.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return hash, err
}

func (r *AccountRepo) ListUsersByTenant(ctx context.Context, tenantID uuid.UUID, page, pageSize int) ([]domain.User, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, email, name, role, status, email_verified, last_login_at, created_at, updated_at
		 FROM users WHERE tenant_id = $1 AND status != 'DELETED'
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		tenantID, pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Status,
			&u.EmailVerified, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}

	var total int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND status != 'DELETED'`, tenantID,
	).Scan(&total)

	return users, total, nil
}

func (r *AccountRepo) UpdateUserProfile(ctx context.Context, userID uuid.UUID, name string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET name = $1, updated_at = NOW() WHERE id = $2`, name, userID)
	return err
}

func (r *AccountRepo) UpdateUserPassword(ctx context.Context, userID uuid.UUID, newHash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`, newHash, userID)
	return err
}

func (r *AccountRepo) UpdateUserRole(ctx context.Context, userID uuid.UUID, role domain.UserRole) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`, role, userID)
	return err
}

func (r *AccountRepo) UpdateUserStatus(ctx context.Context, userID uuid.UUID, status domain.UserStatus) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET status = $1, updated_at = NOW() WHERE id = $2`, status, userID)
	return err
}

func (r *AccountRepo) MarkEmailVerified(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET email_verified = TRUE, status = 'ACTIVE', updated_at = NOW() WHERE id = $1`, userID)
	return err
}

func (r *AccountRepo) RecordLoginSuccess(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET failed_login_attempts = 0, last_login_at = NOW(), locked_until = NULL, updated_at = NOW() WHERE id = $1`,
		userID)
	return err
}

func (r *AccountRepo) RecordLoginFailure(ctx context.Context, userID uuid.UUID, maxAttempts, lockoutMinutes int) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET
		   failed_login_attempts = failed_login_attempts + 1,
		   locked_until = CASE
		     WHEN failed_login_attempts + 1 >= $2
		     THEN NOW() + ($3 || ' minutes')::interval
		     ELSE locked_until
		   END,
		   updated_at = NOW()
		 WHERE id = $1`,
		userID, maxAttempts, lockoutMinutes)
	return err
}

func (r *AccountRepo) SoftDeleteUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET status = 'DELETED', updated_at = NOW() WHERE id = $1`, userID)
	return err
}

// ─── Refresh Tokens ───────────────────────────────────────────────────────────

func (r *AccountRepo) CreateRefreshToken(ctx context.Context, t *domain.RefreshToken) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.UserID, t.TenantID, t.TokenHash, t.ExpiresAt, t.CreatedAt,
	)
	return err
}

func (r *AccountRepo) GetRefreshToken(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, tenant_id, token_hash, expires_at, revoked, created_at
		 FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TenantID, &t.TokenHash, &t.ExpiresAt, &t.Revoked, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *AccountRepo) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash)
	return err
}

func (r *AccountRepo) RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, userID)
	return err
}

// ─── Email Verification ───────────────────────────────────────────────────────

func (r *AccountRepo) CreateEmailVerification(ctx context.Context, v *domain.EmailVerification) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO email_verifications (id, user_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		v.ID, v.UserID, v.TokenHash, v.ExpiresAt, v.CreatedAt,
	)
	return err
}

func (r *AccountRepo) GetEmailVerification(ctx context.Context, tokenHash string) (*domain.EmailVerification, error) {
	var v domain.EmailVerification
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM email_verifications WHERE token_hash = $1`, tokenHash,
	).Scan(&v.ID, &v.UserID, &v.TokenHash, &v.ExpiresAt, &v.UsedAt, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &v, err
}

func (r *AccountRepo) MarkEmailVerificationUsed(ctx context.Context, tokenHash string) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(ctx,
		`UPDATE email_verifications SET used_at = $1 WHERE token_hash = $2`, now, tokenHash)
	return err
}

// ─── Password Reset ───────────────────────────────────────────────────────────

func (r *AccountRepo) CreatePasswordReset(ctx context.Context, p *domain.PasswordResetToken) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		p.ID, p.UserID, p.TokenHash, p.ExpiresAt, p.CreatedAt,
	)
	return err
}

func (r *AccountRepo) GetPasswordReset(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	var p domain.PasswordResetToken
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM password_reset_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&p.ID, &p.UserID, &p.TokenHash, &p.ExpiresAt, &p.UsedAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *AccountRepo) MarkPasswordResetUsed(ctx context.Context, tokenHash string) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = $1 WHERE token_hash = $2`, now, tokenHash)
	return err
}

// ─── API Keys ─────────────────────────────────────────────────────────────────

func (r *AccountRepo) CreateAPIKey(ctx context.Context, k *domain.APIKey) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, key_prefix, scopes, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		k.ID, k.TenantID, k.UserID, k.Name, k.KeyHash, k.KeyPrefix, k.Scopes, k.ExpiresAt, k.CreatedAt,
	)
	return err
}

func (r *AccountRepo) ListAPIKeysByUser(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, user_id, name, key_prefix, scopes, expires_at, revoked, last_used_at, created_at
		 FROM api_keys WHERE user_id = $1 AND revoked = FALSE ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []domain.APIKey
	for rows.Next() {
		var k domain.APIKey
		if err := rows.Scan(&k.ID, &k.TenantID, &k.UserID, &k.Name, &k.KeyPrefix,
			&k.Scopes, &k.ExpiresAt, &k.Revoked, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (r *AccountRepo) RevokeAPIKey(ctx context.Context, keyID, userID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`UPDATE api_keys SET revoked = TRUE WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate key") ||
		strings.Contains(err.Error(), "unique constraint")
}

// ─── Google OAuth ─────────────────────────────────────────────────────────────

// GetUserByGoogleID finds a user by their Google OAuth subject ID.
func (r *AccountRepo) GetUserByGoogleID(ctx context.Context, googleID string) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, email, name, role, status, email_verified,
		        profile_photo_key, google_id,
		        failed_login_attempts, locked_until, last_login_at, created_at, updated_at
		 FROM users WHERE google_id = $1 AND status != 'DELETED'`, googleID,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.EmailVerified, &u.ProfilePhotoKey, &u.GoogleID,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &u, err
}

// UpsertGoogleUser creates or updates a user that signed in with Google.
// Returns the user and whether they were newly created.
func (r *AccountRepo) UpsertGoogleUser(ctx context.Context, u *domain.User) (bool, error) {
	var existing domain.User
	err := r.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1 AND status != 'DELETED'`, u.Email,
	).Scan(&existing.ID)

	if errors.Is(err, pgx.ErrNoRows) {
		// New user — create account
		_, err = r.db.Exec(ctx,
			`INSERT INTO users (id, tenant_id, email, password_hash, name, role, status,
			                    email_verified, google_id, profile_photo_key, created_at, updated_at)
			 VALUES ($1, $2, $3, NULL, $4, $5, $6, TRUE, $7, $8, $9, $10)`,
			u.ID, u.TenantID, u.Email, u.Name, u.Role, u.Status,
			u.GoogleID, u.ProfilePhotoKey, u.CreatedAt, u.UpdatedAt,
		)
		return true, err
	}
	if err != nil {
		return false, err
	}

	// Existing user — link Google ID and update profile photo if not set
	_, err = r.db.Exec(ctx,
		`UPDATE users
		 SET google_id = $1,
		     email_verified = TRUE,
		     profile_photo_key = COALESCE(profile_photo_key, $2),
		     updated_at = NOW()
		 WHERE email = $3`,
		u.GoogleID, u.ProfilePhotoKey, u.Email,
	)
	// Return the existing user by email
	if err == nil {
		existing2, err2 := r.GetUserByGoogleID(ctx, *u.GoogleID)
		if err2 == nil {
			*u = *existing2
		}
	}
	return false, err
}

// ─── Profile Photo ────────────────────────────────────────────────────────────

// UpdateProfilePhotoKey stores the MinIO object key for the user's profile photo.
func (r *AccountRepo) UpdateProfilePhotoKey(ctx context.Context, userID uuid.UUID, objectKey string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET profile_photo_key = $1, updated_at = NOW() WHERE id = $2`,
		objectKey, userID,
	)
	return err
}
