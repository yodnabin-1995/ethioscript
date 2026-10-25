package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"go.uber.org/zap"
)

// authLogger is a fallback logger used by writeUseCaseError (package-level helper).
// Handlers that have h.log should prefer that; this covers unexpected 500s.
var authLogger, _ = zap.NewProduction()

// AuthHandler handles registration, login, token refresh, logout, and profile.
type AuthHandler struct {
	uc  *usecase.AccountUseCase
	log *zap.Logger
}

func NewAuthHandler(uc *usecase.AccountUseCase, log *zap.Logger) *AuthHandler {
	return &AuthHandler{uc: uc, log: log}
}

// Register godoc
//
//	@Summary		Register a new user
//	@Description	Creates a user account within an existing tenant and returns a token pair
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.RegisterRequest	true	"Registration payload"
//	@Success		201		{object}	domain.TokenResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		409		{object}	domain.ErrorResponse
//	@Router			/auth/register [post]
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req domain.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	resp, err := h.uc.Register(r.Context(), req)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, resp)
}

// Login godoc
//
//	@Summary		Authenticate user
//	@Description	Validates credentials and returns JWT access + refresh token pair
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.LoginRequest	true	"Login credentials"
//	@Success		200		{object}	domain.TokenResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/auth/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req domain.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	resp, err := h.uc.Login(r.Context(), req)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, resp)
}

// RefreshToken godoc
//
//	@Summary		Refresh access token
//	@Description	Exchanges a valid refresh token for a new rotated access + refresh token pair
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.RefreshRequest	true	"Refresh token"
//	@Success		200		{object}	domain.TokenResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/auth/refresh [post]
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req domain.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	resp, err := h.uc.RefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, resp)
}

// Logout godoc
//
//	@Summary		Logout (current device)
//	@Description	Revokes the provided refresh token
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	domain.RefreshRequest	true	"Refresh token to revoke"
//	@Success		204		"Logged out"
//	@Router			/auth/logout [post]
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req domain.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	_ = h.uc.Logout(r.Context(), req.RefreshToken)
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll godoc
//
//	@Summary		Logout all devices
//	@Description	Revokes all refresh tokens for the authenticated user
//	@Tags			auth
//	@Security		BearerAuth
//	@Success		204	"All sessions revoked"
//	@Router			/auth/logout-all [post]
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	_ = h.uc.LogoutAll(r.Context(), claims.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// Me godoc
//
//	@Summary		Get current user profile
//	@Description	Returns the authenticated user's full profile
//	@Tags			auth
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	domain.User
//	@Failure		401	{object}	domain.ErrorResponse
//	@Router			/auth/me [get]
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	user, err := h.uc.GetProfile(r.Context(), claims.UserID)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, user)
}

// UpdateProfile godoc
//
//	@Summary		Update profile
//	@Description	Updates the authenticated user's display name
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.UpdateProfileRequest	true	"Profile update"
//	@Success		200		{object}	domain.User
//	@Router			/auth/me [put]
func (h *AuthHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	var req domain.UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	user, err := h.uc.UpdateProfile(r.Context(), claims.UserID, req)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, user)
}

// ─── Password endpoints ───────────────────────────────────────────────────────

// ChangePassword godoc
//
//	@Summary		Change password
//	@Description	Changes the authenticated user's password and revokes all sessions
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	domain.ChangePasswordRequest	true	"Password change"
//	@Success		204		"Password changed"
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/auth/change-password [post]
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	var req domain.ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.uc.ChangePassword(r.Context(), claims.UserID, req); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ForgotPassword godoc
//
//	@Summary		Request password reset
//	@Description	Sends a password reset link to the user's email (always returns 204 to prevent enumeration)
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	domain.ForgotPasswordRequest	true	"Email address"
//	@Success		204		"Reset email sent if account exists"
//	@Router			/auth/forgot-password [post]
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req domain.ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	_ = h.uc.ForgotPassword(r.Context(), req) // always succeed silently
	w.WriteHeader(http.StatusNoContent)
}

// ResetPassword godoc
//
//	@Summary		Reset password with token
//	@Description	Sets a new password using a valid password-reset token
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	domain.ResetPasswordRequest	true	"Reset token + new password"
//	@Success		204		"Password reset"
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/auth/reset-password [post]
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req domain.ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.uc.ResetPassword(r.Context(), req); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Email Verification ───────────────────────────────────────────────────────

// SendVerificationEmail godoc
//
//	@Summary		Resend verification email
//	@Description	Sends a new email verification link to the authenticated user
//	@Tags			auth
//	@Security		BearerAuth
//	@Success		204	"Verification email sent"
//	@Router			/auth/send-verification [post]
func (h *AuthHandler) SendVerificationEmail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	if err := h.uc.SendVerificationEmail(r.Context(), claims.UserID); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VerifyEmail godoc
//
//	@Summary		Verify email address
//	@Description	Verifies the user's email using the token sent to their inbox
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	domain.VerifyEmailRequest	true	"Verification token"
//	@Success		204		"Email verified"
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/auth/verify-email [post]
func (h *AuthHandler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req domain.VerifyEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.uc.VerifyEmail(r.Context(), req); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Error mapping helper ─────────────────────────────────────────────────────

func writeUseCaseError(w http.ResponseWriter, r *http.Request, err error) {
	traceID := middleware.GetTraceID(r.Context())
	switch {
	case errors.Is(err, domain.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, err.Error(), "NOT_FOUND")
	case errors.Is(err, domain.ErrConflict):
		middleware.WriteError(w, r, http.StatusConflict, err.Error(), "CONFLICT")
	case errors.Is(err, domain.ErrValidation):
		middleware.WriteError(w, r, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
	case errors.Is(err, domain.ErrWrongPassword), errors.Is(err, domain.ErrUnauthorized):
		// Don't log wrong passwords — expected, noisy
		middleware.WriteError(w, r, http.StatusUnauthorized, "invalid credentials", "AUTH_FAILED")
	case errors.Is(err, domain.ErrForbidden):
		middleware.WriteError(w, r, http.StatusForbidden, err.Error(), "FORBIDDEN")
	case errors.Is(err, domain.ErrAccountSuspended):
		middleware.WriteError(w, r, http.StatusForbidden, "account suspended", "SUSPENDED")
	case errors.Is(err, domain.ErrTokenExpired):
		middleware.WriteError(w, r, http.StatusUnauthorized, "token expired or invalid", "TOKEN_EXPIRED")
	case errors.Is(err, domain.ErrPasswordPolicy):
		middleware.WriteError(w, r, http.StatusBadRequest, err.Error(), "PASSWORD_POLICY")
	default:
		// 500 — unexpected error, always log with full details
		authLogger.Error("unhandled usecase error",
			zap.Error(err),
			zap.String("method",   r.Method),
			zap.String("path",     r.URL.Path),
			zap.String("trace_id", traceID),
		)
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal server error", "INTERNAL_ERROR")
	}
}
