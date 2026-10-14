package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthHandler provides liveness and readiness probes.
type HealthHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func NewHealthHandler(db *pgxpool.Pool, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{db: db, redis: rdb}
}

// Health godoc
//
//	@Summary		Health check
//	@Description	Returns account-service health and dependency status
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	domain.HealthResponse
//	@Failure		503	{object}	domain.HealthResponse
//	@Router			/health [get]
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	svcs := make(map[string]string)
	ok := true

	if err := h.db.Ping(ctx); err != nil {
		svcs["postgres"] = "unhealthy: " + err.Error()
		ok = false
	} else {
		svcs["postgres"] = "healthy"
	}

	if err := h.redis.Ping(ctx).Err(); err != nil {
		svcs["redis"] = "unhealthy: " + err.Error()
		ok = false
	} else {
		svcs["redis"] = "healthy"
	}

	status := "ok"
	code := http.StatusOK
	if !ok {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	middleware.WriteJSON(w, code, domain.HealthResponse{
		Status:    status,
		Version:   "3.1.0",
		Timestamp: time.Now().UTC(),
		Services:  svcs,
	})
}

// Readiness godoc
//
//	@Summary		Readiness probe
//	@Description	Kubernetes readiness — confirms DB is reachable
//	@Tags			system
//	@Success		200
//	@Failure		503
//	@Router			/ready [get]
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
