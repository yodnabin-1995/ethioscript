package handler

import (
	"net/http"
	"os"

	"github.com/ethioscript/notification-service/internal/config"
	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/middleware"
	"github.com/ethioscript/notification-service/internal/repository"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

const openapiSpecPath = "/app/docs/openapi.yml"

// NewRouter wires all routes for the notification-service.
func NewRouter(
	cfg *config.Config,
	repo *repository.NotificationRepo,
	log *zap.Logger,
) http.Handler {
	r := chi.NewRouter()

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger(log))
	r.Use(middleware.Recoverer(log))
	r.Use(middleware.SecureHeaders)
	r.Use(chimiddleware.RealIP)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Trace-ID", "X-Internal-Key"},
		ExposedHeaders:   []string{"X-Trace-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(httprate.LimitByIP(100, 1))

	// ── Handlers ──────────────────────────────────────────────────────────────
	notifH  := NewNotificationHandler(repo, log)
	pushH   := NewPushHandler(repo, cfg, log)
	otpH    := NewOTPHandler(repo, cfg, log)
	healthH := NewHealthHandler(repo.DB())

	// ── System routes (no auth) ───────────────────────────────────────────────
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/health", healthH.Health)
	r.Handle("/metrics", promhttp.Handler())

	// ── Swagger UI ────────────────────────────────────────────────────────────
	if cfg.Swagger.Enabled {
		r.Get("/docs/openapi.yml", func(w http.ResponseWriter, r *http.Request) {
			data, err := os.ReadFile(openapiSpecPath)
			if err != nil {
				http.Error(w, "openapi spec not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(data)
		})

		swaggerHandler := httpSwagger.Handler(
			httpSwagger.URL("/docs/openapi.yml?v=1"),
			httpSwagger.DeepLinking(true),
			httpSwagger.DocExpansion("list"),
			httpSwagger.PersistAuthorization(true),
		)
		r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/index.html", http.StatusFound)
		})
		r.Get("/docs/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/index.html", http.StatusFound)
		})
		r.Get("/docs/*", swaggerHandler)
	}

	// ══════════════════════════════════════════════════════════════════════════
	// PUBLIC — OTP (no auth: send/verify called by account-service flow)
	// ══════════════════════════════════════════════════════════════════════════
	r.Post("/notifications/otp/send",   otpH.SendOTP)
	r.Post("/notifications/otp/verify", otpH.VerifyOTP)

	// VAPID public key — no auth needed, browser reads it before subscribing
	r.Get("/notifications/push/vapid-public-key", pushH.GetVAPIDPublicKey)

	// ══════════════════════════════════════════════════════════════════════════
	// USER — Bearer JWT required
	// ══════════════════════════════════════════════════════════════════════════
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.Auth.JWTSecret))

		// Inbox
		r.Get("/notifications",                  notifH.GetInbox)
		r.Get("/notifications/unread-count",     notifH.GetUnreadCount)
		r.Post("/notifications/mark-all-read",   notifH.MarkAllRead)
		r.Patch("/notifications/{id}/read",      notifH.MarkRead)

		// Push subscriptions
		r.Post("/notifications/push/subscribe",   pushH.Subscribe)
		r.Delete("/notifications/push/subscribe", pushH.Unsubscribe)
	})

	// ══════════════════════════════════════════════════════════════════════════
	// ADMIN — Bearer JWT + ADMIN role
	// ══════════════════════════════════════════════════════════════════════════
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.Auth.JWTSecret))
		r.Use(middleware.RequireRole(domain.RoleAdmin))
		r.Get("/admin/notifications/stats", notifH.AdminStats)
	})

	// ══════════════════════════════════════════════════════════════════════════
	// INTERNAL — X-Internal-Key header required
	// ══════════════════════════════════════════════════════════════════════════
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireInternalKey(cfg.Auth.InternalAPIKey))

		r.Post("/internal/notification-events", notifH.CreateInternalEvent)
		r.Post("/notifications/bulk",           notifH.SendBulk)
		r.Post("/notifications/email",          notifH.SendEmail)
		r.Post("/notifications/sms",            notifH.SendSMS)
		r.Post("/notifications/telegram",       notifH.SendTelegram)
	})

	return r
}
