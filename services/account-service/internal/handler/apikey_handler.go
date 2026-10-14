package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// APIKeyHandler manages API key creation, listing, and revocation.
type APIKeyHandler struct {
	uc  *usecase.AccountUseCase
	log *zap.Logger
}

func NewAPIKeyHandler(uc *usecase.AccountUseCase, log *zap.Logger) *APIKeyHandler {
	return &APIKeyHandler{uc: uc, log: log}
}

// CreateAPIKey godoc
//
//	@Summary		Create API key
//	@Description	Creates a new API key for the authenticated user. The raw key is shown ONCE — store it securely.
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.CreateAPIKeyRequest	true	"API key config"
//	@Success		201		{object}	domain.CreateAPIKeyResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/api-keys [post]
func (h *APIKeyHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	var req domain.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	resp, err := h.uc.CreateAPIKey(r.Context(), claims.TenantID, claims.UserID, req)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, resp)
}

// ListAPIKeys godoc
//
//	@Summary		List API keys
//	@Description	Returns all active API keys for the authenticated user (raw key is never returned in listing)
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		domain.APIKey
//	@Router			/api-keys [get]
func (h *APIKeyHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	keys, err := h.uc.ListAPIKeys(r.Context(), claims.UserID)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, keys)
}

// RevokeAPIKey godoc
//
//	@Summary		Revoke API key
//	@Description	Permanently revokes an API key by ID
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Param			id	path	string	true	"API key UUID"
//	@Success		204	"Key revoked"
//	@Failure		404	{object}	domain.ErrorResponse
//	@Router			/api-keys/{id} [delete]
func (h *APIKeyHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	keyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid key id", "BAD_REQUEST")
		return
	}
	if err := h.uc.RevokeAPIKey(r.Context(), keyID, claims.UserID); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
