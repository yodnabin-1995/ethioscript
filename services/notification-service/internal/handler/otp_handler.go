package handler

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/ethioscript/notification-service/internal/config"
	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/middleware"
	"github.com/ethioscript/notification-service/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// OTPHandler handles OTP send and verify endpoints.
type OTPHandler struct {
	repo *repository.NotificationRepo
	cfg  *config.Config
	log  *zap.Logger
}

func NewOTPHandler(repo *repository.NotificationRepo, cfg *config.Config, log *zap.Logger) *OTPHandler {
	return &OTPHandler{repo: repo, cfg: cfg, log: log}
}

// SendOTP godoc
//
//	@Summary		Send OTP code
//	@Description	Generates a time-limited one-time password and sends it to the target (email or phone). In development the code is logged — connect an SMTP/SMS provider for production.
//	@Tags			otp
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.OTPSendRequest	true	"OTP send request"
//	@Success		200		{object}	domain.OTPSendResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		429		{object}	domain.ErrorResponse
//	@Router			/notifications/otp/send [post]
func (h *OTPHandler) SendOTP(w http.ResponseWriter, r *http.Request) {
	var req domain.OTPSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.Target == "" {
		middleware.WriteError(w, r, http.StatusBadRequest, "target is required", "BAD_REQUEST")
		return
	}

	code := generateOTP(h.cfg.OTP.Length)
	hash, err := bcrypt.GenerateFromPassword([]byte(code), 10)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not generate otp", "INTERNAL")
		return
	}

	expiresAt := time.Now().UTC().Add(h.cfg.OTPExpiry())

	var userID *uuid.UUID
	if req.UserID != nil && *req.UserID != "" {
		uid, err := uuid.Parse(*req.UserID)
		if err == nil {
			userID = &uid
		}
	}

	if err := h.repo.CreateOTP(r.Context(), userID, req.Target, req.Purpose, string(hash), expiresAt); err != nil {
		h.log.Error("create otp", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not create otp", "INTERNAL")
		return
	}

	// DEV: log the code. Replace with SMTP/SMS provider call in production.
	h.log.Info("OTP generated [DEV ONLY — replace with provider]",
		zap.String("target", req.Target),
		zap.String("purpose", string(req.Purpose)),
		zap.String("code", code),
	)

	middleware.WriteJSON(w, http.StatusOK, domain.OTPSendResponse{
		Message:      "OTP sent successfully",
		ExpiresAt:    expiresAt,
		MaskedTarget: maskTarget(req.Target),
	})
}

// VerifyOTP godoc
//
//	@Summary		Verify OTP code
//	@Description	Validates the submitted OTP code. Returns valid=true if the code matches and has not expired.
//	@Tags			otp
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.OTPVerifyRequest		true	"OTP verify request"
//	@Success		200		{object}	domain.OTPVerifyResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/notifications/otp/verify [post]
func (h *OTPHandler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req domain.OTPVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	otp, err := h.repo.GetActiveOTP(r.Context(), req.Target, req.Purpose)
	if err == domain.ErrNotFound {
		middleware.WriteJSON(w, http.StatusOK, domain.OTPVerifyResponse{
			Valid:   false,
			Message: "no active OTP found for this target and purpose",
		})
		return
	}
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not verify otp", "INTERNAL")
		return
	}

	if time.Now().UTC().After(otp.ExpiresAt) {
		middleware.WriteJSON(w, http.StatusOK, domain.OTPVerifyResponse{Valid: false, Message: "OTP has expired"})
		return
	}

	if otp.Attempts >= otp.MaxAttempts {
		middleware.WriteJSON(w, http.StatusOK, domain.OTPVerifyResponse{Valid: false, Message: "too many failed attempts"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(otp.CodeHash), []byte(req.Code)); err != nil {
		_ = h.repo.IncrementOTPAttempts(r.Context(), otp.ID)
		middleware.WriteJSON(w, http.StatusOK, domain.OTPVerifyResponse{Valid: false, Message: "invalid OTP code"})
		return
	}

	_ = h.repo.MarkOTPUsed(r.Context(), otp.ID)
	middleware.WriteJSON(w, http.StatusOK, domain.OTPVerifyResponse{Valid: true, Message: "OTP verified successfully"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func generateOTP(length int) string {
	digits := "0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = digits[rand.Intn(len(digits))]
	}
	return string(b)
}

func maskTarget(target string) string {
	if idx := strings.Index(target, "@"); idx >= 0 {
		local := target[:idx]
		domain := target[idx:]
		if len(local) > 2 {
			return local[:2] + strings.Repeat("*", len(local)-2) + domain
		}
		return "*" + domain
	}
	if len(target) > 5 {
		return target[:3] + strings.Repeat("*", len(target)-5) + target[len(target)-2:]
	}
	return fmt.Sprintf("%s***", target[:min(2, len(target))])
}
