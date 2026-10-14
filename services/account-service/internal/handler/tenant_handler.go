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

// TenantHandler manages tenant CRUD (Platform Operator only).
type TenantHandler struct {
	uc  *usecase.AccountUseCase
	log *zap.Logger
}

func NewTenantHandler(uc *usecase.AccountUseCase, log *zap.Logger) *TenantHandler {
	return &TenantHandler{uc: uc, log: log}
}

// CreateTenant godoc
//
//	@Summary		Create tenant
//	@Description	Creates a new tenant organisation. Platform Operator only.
//	@Tags			tenants
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.CreateTenantRequest	true	"Tenant details"
//	@Success		201		{object}	domain.Tenant
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		409		{object}	domain.ErrorResponse
//	@Router			/tenants [post]
func (h *TenantHandler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	tenant, err := h.uc.CreateTenant(r.Context(), req)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, tenant)
}

// ListTenants godoc
//
//	@Summary		List all tenants
//	@Description	Returns all tenants. Platform Operator only.
//	@Tags			tenants
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		domain.Tenant
//	@Router			/tenants [get]
func (h *TenantHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.uc.ListTenants(r.Context())
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, tenants)
}

// GetTenant godoc
//
//	@Summary		Get tenant by ID
//	@Tags			tenants
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Tenant UUID"
//	@Success		200	{object}	domain.Tenant
//	@Failure		404	{object}	domain.ErrorResponse
//	@Router			/tenants/{id} [get]
func (h *TenantHandler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid tenant id", "BAD_REQUEST")
		return
	}
	tenant, err := h.uc.GetTenant(r.Context(), tenantID)
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, tenant)
}
