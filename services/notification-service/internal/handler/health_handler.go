package handler

import (
	"net/http"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthHandler handles /health and /ready.
type HealthHandler struct {
	db *pgxpool.Pool
}

func NewHealthHandler(db *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{db: db}
}

// Health godoc
//
//	@Summary		Service health check
//	@Description	Returns service status and dependency health
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	domain.HealthResponse
//	@Router			/health [get]
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	services := map[string]string{}

	dbStatus := "ok"
	if err := h.db.Ping(r.Context()); err != nil {
		dbStatus = "unavailable: " + err.Error()
	}
	services["postgres"] = dbStatus

	overall := "ok"
	if dbStatus != "ok" {
		overall = "degraded"
	}

	middleware.WriteJSON(w, http.StatusOK, domain.HealthResponse{
		Status:    overall,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Services:  services,
	})
}
