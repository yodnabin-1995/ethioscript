package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/ethioscript/account-service/internal/auth"
	"github.com/ethioscript/account-service/internal/config"
	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AccountUseCase contains all account business logic.
type AccountUseCase struct {
	repo   *repository.AccountRepo
	jwt    *auth.Manager
	cfg    *config.Config
	log    *zap.Logger
}

func NewAccountUseCase(repo *repository.AccountRepo, jwt *auth.Manager, cfg *config.Config, log *zap.Logger) *AccountUseCase {
	return &AccountUseCase{repo: repo, jwt: jwt, cfg: cfg, log: log}
}

// ─── Registration ─────────────────────────────────────────────────────────────

func (uc *AccountUseCase) Register(ctx context.Context, req domain.RegisterRequest) (*domain.TokenResponse, error) {
	if req.Email == "" || req.Password == "" || req.Name == "" {
		return nil, fmt.Errorf("%w: email, password, and name are required", domain.ErrValidation)
	}

	// Default to the "default" tenant when none is specified
	if req.TenantSlug == "" {
		req.TenantSlug = "default"
	}

	tenant, err := uc.repo.GetTenantBySlug(ctx, req.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant '%s' not found", domain.ErrNotFound, req.TenantSlug)
	}
	if !tenant.IsActive {
		return nil, fmt.Errorf("%w: tenant is inactive", domain.ErrForbidden)
	}

	hash, err := auth.HashPassword(req.Password, uc.cfg.Security.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrPasswordPolicy, err.Error())
	}

	now := time.Now().UTC()
	user := &domain.User{
		ID:        uuid.New(),
		TenantID:  tenant.ID,
		Email:     req.Email,
		Name:      req.Name,
		Role:      domain.RoleContributor,
		Status:    domain.StatusPendingVerification,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := uc.repo.CreateUser(ctx, user, hash); err != nil {
		return nil, fmt.Errorf("%w: email already registered", domain.ErrConflict)
	}

	// Issue token pair (user active immediately for MVP; email verify is async)
	user.Status = domain.StatusActive
	return uc.issueTokenPair(ctx, *user)
}

// ─── Login ────────────────────────────────────────────────────────────────────

func (uc *AccountUseCase) Login(ctx context.Context, req domain.LoginRequest) (*domain.TokenResponse, error) {
	user, hash, err := uc.repo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		return nil, domain.ErrWrongPassword // don't leak whether email exists
	}

	if user.Status == domain.StatusSuspended {
		return nil, domain.ErrAccountSuspended
	}
	if user.Status == domain.StatusDeleted {
		return nil, domain.ErrWrongPassword
	}
	if user.IsLocked() {
		return nil, fmt.Errorf("account locked; try again after %s", user.LockedUntil.Format(time.RFC3339))
	}

	if err := auth.CheckPassword(req.Password, hash); err != nil {
		_ = uc.repo.RecordLoginFailure(ctx, user.ID, uc.cfg.Security.MaxLoginAttempts, uc.cfg.Security.LockoutMinutes)
		return nil, domain.ErrWrongPassword
	}

	_ = uc.repo.RecordLoginSuccess(ctx, user.ID)
	return uc.issueTokenPair(ctx, *user)
}

// ─── Token Refresh ────────────────────────────────────────────────────────────

func (uc *AccountUseCase) RefreshToken(ctx context.Context, rawToken string) (*domain.TokenResponse, error) {
	tokenHash := auth.HashToken(rawToken)

	rt, err := uc.repo.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return nil, domain.ErrTokenExpired
	}
	if rt.Revoked || time.Now().After(rt.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	// Rotate: revoke old, issue new
	_ = uc.repo.RevokeRefreshToken(ctx, tokenHash)

	user, err := uc.repo.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return nil, domain.ErrNotFound
	}

	return uc.issueTokenPair(ctx, *user)
}

// ─── Logout ───────────────────────────────────────────────────────────────────

func (uc *AccountUseCase) Logout(ctx context.Context, rawToken string) error {
	tokenHash := auth.HashToken(rawToken)
	return uc.repo.RevokeRefreshToken(ctx, tokenHash)
}

// LogoutAll revokes all refresh tokens for the user (sign out everywhere).
func (uc *AccountUseCase) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return uc.repo.RevokeAllUserRefreshTokens(ctx, userID)
}

// ─── Profile ──────────────────────────────────────────────────────────────────

func (uc *AccountUseCase) GetProfile(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	return uc.repo.GetUserByID(ctx, userID)
}

func (uc *AccountUseCase) UpdateProfile(ctx context.Context, userID uuid.UUID, req domain.UpdateProfileRequest) (*domain.User, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}
	if err := uc.repo.UpdateUserProfile(ctx, userID, req.Name); err != nil {
		return nil, err
	}
	return uc.repo.GetUserByID(ctx, userID)
}

// ─── Password ─────────────────────────────────────────────────────────────────

func (uc *AccountUseCase) ChangePassword(ctx context.Context, userID uuid.UUID, req domain.ChangePasswordRequest) error {
	currentHash, err := uc.repo.GetUserPasswordHash(ctx, userID)
	if err != nil {
		return domain.ErrNotFound
	}
	if err := auth.CheckPassword(req.CurrentPassword, currentHash); err != nil {
		return domain.ErrWrongPassword
	}
	newHash, err := auth.HashPassword(req.NewPassword, uc.cfg.Security.BcryptCost)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrPasswordPolicy, err.Error())
	}
	if err := uc.repo.UpdateUserPassword(ctx, userID, newHash); err != nil {
		return err
	}
	// Revoke all refresh tokens to force re-login on all devices
	return uc.repo.RevokeAllUserRefreshTokens(ctx, userID)
}

func (uc *AccountUseCase) ForgotPassword(ctx context.Context, req domain.ForgotPasswordRequest) error {
	user, _, err := uc.repo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// Don't reveal whether email exists — silently succeed
		uc.log.Info("forgot password: email not found (silent)", zap.String("email", req.Email))
		return nil
	}

	raw, hash, err := auth.GenerateOpaqueToken()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	prt := &domain.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: now.Add(uc.cfg.Auth.PasswordResetExpiryMin),
		CreatedAt: now,
	}
	if err := uc.repo.CreatePasswordReset(ctx, prt); err != nil {
		return err
	}

	// In production: send email with raw token link
	uc.log.Info("password reset token generated (send via email)",
		zap.String("user_id", user.ID.String()),
		zap.String("token_preview", raw[:8]+"…"),
	)
	return nil
}

func (uc *AccountUseCase) ResetPassword(ctx context.Context, req domain.ResetPasswordRequest) error {
	tokenHash := auth.HashToken(req.Token)
	prt, err := uc.repo.GetPasswordReset(ctx, tokenHash)
	if err != nil || prt.UsedAt != nil || time.Now().After(prt.ExpiresAt) {
		return domain.ErrTokenExpired
	}

	newHash, err := auth.HashPassword(req.NewPassword, uc.cfg.Security.BcryptCost)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrPasswordPolicy, err.Error())
	}

	if err := uc.repo.UpdateUserPassword(ctx, prt.UserID, newHash); err != nil {
		return err
	}
	_ = uc.repo.MarkPasswordResetUsed(ctx, tokenHash)
	_ = uc.repo.RevokeAllUserRefreshTokens(ctx, prt.UserID)
	return nil
}

// ─── Email Verification ───────────────────────────────────────────────────────

func (uc *AccountUseCase) SendVerificationEmail(ctx context.Context, userID uuid.UUID) error {
	raw, hash, err := auth.GenerateOpaqueToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ev := &domain.EmailVerification{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: now.Add(uc.cfg.Auth.EmailVerifyExpiryHours),
		CreatedAt: now,
	}
	if err := uc.repo.CreateEmailVerification(ctx, ev); err != nil {
		return err
	}
	uc.log.Info("email verification token generated (send via email)",
		zap.String("user_id", userID.String()),
		zap.String("token_preview", raw[:8]+"…"),
	)
	return nil
}

func (uc *AccountUseCase) VerifyEmail(ctx context.Context, req domain.VerifyEmailRequest) error {
	tokenHash := auth.HashToken(req.Token)
	ev, err := uc.repo.GetEmailVerification(ctx, tokenHash)
	if err != nil || ev.UsedAt != nil || time.Now().After(ev.ExpiresAt) {
		return domain.ErrTokenExpired
	}
	if err := uc.repo.MarkEmailVerified(ctx, ev.UserID); err != nil {
		return err
	}
	return uc.repo.MarkEmailVerificationUsed(ctx, tokenHash)
}

// ─── Tenant Management ────────────────────────────────────────────────────────

func (uc *AccountUseCase) CreateTenant(ctx context.Context, req domain.CreateTenantRequest) (*domain.Tenant, error) {
	if req.Name == "" || req.Slug == "" {
		return nil, fmt.Errorf("%w: name and slug are required", domain.ErrValidation)
	}
	now := time.Now().UTC()
	t := &domain.Tenant{
		ID:        uuid.New(),
		Name:      req.Name,
		Slug:      req.Slug,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := uc.repo.CreateTenant(ctx, t); err != nil {
		return nil, fmt.Errorf("%w: slug already exists", domain.ErrConflict)
	}
	return t, nil
}

func (uc *AccountUseCase) ListTenants(ctx context.Context) ([]domain.Tenant, error) {
	return uc.repo.ListTenants(ctx)
}

func (uc *AccountUseCase) GetTenant(ctx context.Context, tenantID uuid.UUID) (*domain.Tenant, error) {
	return uc.repo.GetTenantByID(ctx, tenantID)
}

// ─── User Management (admin) ──────────────────────────────────────────────────

func (uc *AccountUseCase) ListUsers(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*domain.ListUsersResponse, error) {
	users, total, err := uc.repo.ListUsersByTenant(ctx, tenantID, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &domain.ListUsersResponse{
		Users:    users,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (uc *AccountUseCase) GetUser(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	return uc.repo.GetUserByID(ctx, userID)
}

func (uc *AccountUseCase) UpdateUserRole(ctx context.Context, targetUserID uuid.UUID, req domain.UpdateUserRoleRequest) error {
	validRoles := map[domain.UserRole]bool{
		domain.RoleTenantAdmin:      true,
		domain.RoleContributor:      true,
		domain.RoleReviewer:         true,
		domain.RoleReadOnlyAuditor:  true,
		domain.RolePlatformOperator: true,
	}
	if !validRoles[req.Role] {
		return fmt.Errorf("%w: invalid role", domain.ErrValidation)
	}
	return uc.repo.UpdateUserRole(ctx, targetUserID, req.Role)
}

func (uc *AccountUseCase) SuspendUser(ctx context.Context, targetUserID uuid.UUID) error {
	if err := uc.repo.UpdateUserStatus(ctx, targetUserID, domain.StatusSuspended); err != nil {
		return err
	}
	return uc.repo.RevokeAllUserRefreshTokens(ctx, targetUserID)
}

func (uc *AccountUseCase) ActivateUser(ctx context.Context, targetUserID uuid.UUID) error {
	return uc.repo.UpdateUserStatus(ctx, targetUserID, domain.StatusActive)
}

func (uc *AccountUseCase) DeleteUser(ctx context.Context, targetUserID uuid.UUID) error {
	if err := uc.repo.SoftDeleteUser(ctx, targetUserID); err != nil {
		return err
	}
	return uc.repo.RevokeAllUserRefreshTokens(ctx, targetUserID)
}

// ─── API Keys ─────────────────────────────────────────────────────────────────

func (uc *AccountUseCase) CreateAPIKey(ctx context.Context, tenantID, userID uuid.UUID, req domain.CreateAPIKeyRequest) (*domain.CreateAPIKeyResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}

	raw, hash, err := auth.GenerateOpaqueToken()
	if err != nil {
		return nil, err
	}

	prefix := raw[:8]
	now := time.Now().UTC()
	key := &domain.APIKey{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    userID,
		Name:      req.Name,
		KeyHash:   hash,
		KeyPrefix: prefix,
		Scopes:    req.Scopes,
		ExpiresAt: req.ExpiresAt,
		CreatedAt: now,
	}

	if err := uc.repo.CreateAPIKey(ctx, key); err != nil {
		return nil, err
	}

	return &domain.CreateAPIKeyResponse{
		APIKey: *key,
		RawKey: "es_" + raw, // shown once only
	}, nil
}

func (uc *AccountUseCase) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	return uc.repo.ListAPIKeysByUser(ctx, userID)
}

func (uc *AccountUseCase) RevokeAPIKey(ctx context.Context, keyID, userID uuid.UUID) error {
	return uc.repo.RevokeAPIKey(ctx, keyID, userID)
}

// ─── Token issuer ─────────────────────────────────────────────────────────────

func (uc *AccountUseCase) issueTokenPair(ctx context.Context, user domain.User) (*domain.TokenResponse, error) {
	claims := domain.Claims{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Email:    user.Email,
		Role:     user.Role,
		Name:     user.Name,
	}

	accessToken, expiresAt, err := uc.jwt.GenerateAccessToken(claims)
	if err != nil {
		return nil, err
	}

	rawRefresh, refreshHash, err := auth.GenerateOpaqueToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	rt := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TenantID:  user.TenantID,
		TokenHash: refreshHash,
		ExpiresAt: now.Add(uc.cfg.Auth.RefreshTokenExpiryDays),
		CreatedAt: now,
	}

	if err := uc.repo.CreateRefreshToken(ctx, rt); err != nil {
		return nil, err
	}

	return &domain.TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresAt:    expiresAt,
		TokenType:    "Bearer",
		User:         user,
	}, nil
}
