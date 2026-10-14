package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// UserHandler manages users within a tenant (Tenant Admin / Platform Operator).
type UserHandler struct {
	uc  *usecase.AccountUseCase
	log *zap.Logger
}

func NewUserHandler(uc *usecase.AccountUseCase, log *zap.Logger) *UserHandler {
	return &UserHandler{uc: uc, log: log}
}

// ListUsers godoc
//
//	@Summary		List users in tenant
//	@Description	Returns a paginated list of users within the authenticated user's tenant
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page		query		int	false	"Page number (default 1)"
//	@Param			page_size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200			{object}	domain.ListUsersResponse
//	@Router			/users [get]
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))

	resp, err := h.uc.ListUsers(r.Context(), claims.TenantID, page, pageSize)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, resp)
}

// GetUser godoc
//
//	@Summary		Get user by ID
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"User UUID"
//	@Success		200	{object}	domain.User
//	@Failure		404	{object}	domain.ErrorResponse
//	@Router			/users/{id} [get]
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user id", "BAD_REQUEST")
		return
	}
	user, err := h.uc.GetUser(r.Context(), userID)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, user)
}

// UpdateUserRole godoc
//
//	@Summary		Update user role
//	@Description	Changes a user's role within the tenant. Tenant Admin or Platform Operator only.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path	string							true	"User UUID"
//	@Param			body	body	domain.UpdateUserRoleRequest	true	"New role"
//	@Success		204		"Role updated"
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		404		{object}	domain.ErrorResponse
//	@Router			/users/{id}/role [put]
func (h *UserHandler) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user id", "BAD_REQUEST")
		return
	}
	var req domain.UpdateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.uc.UpdateUserRole(r.Context(), userID, req); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SuspendUser godoc
//
//	@Summary		Suspend user
//	@Description	Suspends a user account and revokes all their sessions
//	@Tags			users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User UUID"
//	@Success		204	"User suspended"
//	@Router			/users/{id}/suspend [post]
func (h *UserHandler) SuspendUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user id", "BAD_REQUEST")
		return
	}
	if err := h.uc.SuspendUser(r.Context(), userID); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ActivateUser godoc
//
//	@Summary		Activate user
//	@Description	Re-activates a suspended user account
//	@Tags			users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User UUID"
//	@Success		204	"User activated"
//	@Router			/users/{id}/activate [post]
func (h *UserHandler) ActivateUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user id", "BAD_REQUEST")
		return
	}
	if err := h.uc.ActivateUser(r.Context(), userID); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteUser godoc
//
//	@Summary		Delete user
//	@Description	Soft-deletes a user account and revokes all sessions
//	@Tags			users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User UUID"
//	@Success		204	"User deleted"
//	@Router			/users/{id} [delete]
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user id", "BAD_REQUEST")
		return
	}
	if err := h.uc.DeleteUser(r.Context(), userID); err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
