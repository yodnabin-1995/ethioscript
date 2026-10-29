package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ethioscript/notification-service/internal/config"
	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/middleware"
	"github.com/ethioscript/notification-service/internal/repository"
	"go.uber.org/zap"
)

// PushHandler handles Web Push / VAPID subscription endpoints.
type PushHandler struct {
	repo *repository.NotificationRepo
	cfg  *config.Config
	log  *zap.Logger
}

func NewPushHandler(repo *repository.NotificationRepo, cfg *config.Config, log *zap.Logger) *PushHandler {
	return &PushHandler{repo: repo, cfg: cfg, log: log}
}

// GetVAPIDPublicKey godoc
//
//	@Summary		Get VAPID public key
//	@Description	Returns the VAPID application server key needed by the browser to create a push subscription
//	@Tags			push
//	@Produce		json
//	@Success		200	{object}	domain.VapidPublicKeyResponse
//	@Router			/notifications/push/vapid-public-key [get]
func (h *PushHandler) GetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	key := h.cfg.VAPID.PublicKey
	if key == "" {
		key = "VAPID_NOT_CONFIGURED"
	}
	middleware.WriteJSON(w, http.StatusOK, domain.VapidPublicKeyResponse{
		VapidPublicKey:       key,
		ApplicationServerKey: key,
	})
}

// Subscribe godoc
//
//	@Summary		Register push subscription
//	@Description	Registers a browser push subscription for the authenticated user
//	@Tags			push
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		domain.PushSubscribeRequest	true	"Push subscription object from browser Push API"
//	@Success		201		{object}	domain.PushSubscribeResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/notifications/push/subscribe [post]
func (h *PushHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	var req domain.PushSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.Endpoint == "" || req.P256DHKey == "" || req.AuthKey == "" {
		middleware.WriteError(w, r, http.StatusBadRequest, "endpoint, p256dh_key, and auth_key are required", "BAD_REQUEST")
		return
	}

	id, err := h.repo.UpsertPushSubscription(r.Context(), userID, req.Endpoint, req.P256DHKey, req.AuthKey, req.UserAgent)
	if err != nil {
		h.log.Error("upsert push subscription", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not register subscription", "INTERNAL")
		return
	}

	h.log.Info("push subscribed", zap.String("user_id", userID.String()), zap.String("endpoint_prefix", req.Endpoint[:min(40, len(req.Endpoint))]))
	middleware.WriteJSON(w, http.StatusCreated, domain.PushSubscribeResponse{
		ID:      id,
		Message: "push subscription registered successfully",
	})
}

// Unsubscribe godoc
//
//	@Summary		Remove push subscription
//	@Description	Removes the push subscription with the given endpoint for the authenticated user
//	@Tags			push
//	@Security		BearerAuth
//	@Param			endpoint	query	string	true	"Endpoint URL of the subscription to remove"
//	@Success		204
//	@Failure		401	{object}	domain.ErrorResponse
//	@Router			/notifications/push/subscribe [delete]
func (h *PushHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	endpoint := r.URL.Query().Get("endpoint")
	if endpoint == "" {
		middleware.WriteError(w, r, http.StatusBadRequest, "endpoint query parameter required", "BAD_REQUEST")
		return
	}

	if err := h.repo.DeletePushSubscription(r.Context(), userID, endpoint); err != nil {
		h.log.Error("delete push subscription", zap.Error(err))
	}

	w.WriteHeader(http.StatusNoContent)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
