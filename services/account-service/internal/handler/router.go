package handler

import (
	"net/http"
	"os"

	"github.com/ethioscript/account-service/internal/auth"
	"github.com/ethioscript/account-service/internal/config"
	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

// NewRouter wires all routes for the account-service.
// specPath is the filesystem path to openapi.yml — read at request time so
// updates to the spec file take effect on container restart, no rebuild needed.
func NewRouter(
	cfg *config.Config,
	db *pgxpool.Pool,
	rdb *redis.Client,
	jwtMgr *auth.Manager,
	uc *usecase.AccountUseCase,
	photoH *PhotoHandler,
	googleH *GoogleHandler,
	log *zap.Logger,
	specPath string,
) http.Handler {

	r := chi.NewRouter()

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger(log))
	r.Use(middleware.Recoverer(log))
	r.Use(middleware.SecureHeaders)
	r.Use(chimiddleware.RealIP)
	// NOTE: StripSlashes intentionally removed — it breaks /docs/ → Swagger UI
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Trace-ID", "Idempotency-Key"},
		ExposedHeaders:   []string{"X-Trace-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(httprate.LimitByIP(cfg.Rate.RPS, 1))

	// ── Handlers ──────────────────────────────────────────────────────────────
	authH    := NewAuthHandler(uc, log)
	tenantH  := NewTenantHandler(uc, log)
	userH    := NewUserHandler(uc, log)
	apiKeyH  := NewAPIKeyHandler(uc, log)
	healthH  := NewHealthHandler(db, rdb)

	// ── System routes (no auth) ───────────────────────────────────────────────
	// Root → redirect to Swagger UI
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/health", healthH.Health)
	r.Get("/ready", healthH.Readiness)
	r.Handle("/metrics", promhttp.Handler())

	// ── Swagger UI — spec served from disk (no rebuild needed on spec changes) ──
	if cfg.Swagger.Enabled {
		r.Get("/docs/openapi.yml", func(w http.ResponseWriter, r *http.Request) {
			data, err := os.ReadFile(specPath)
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
			httpSwagger.URL("/docs/openapi.yml?v=2"),
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
	// AUTH — public endpoints (no JWT required)
	// ══════════════════════════════════════════════════════════════════════════
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register",         authH.Register)
		r.Post("/login",            authH.Login)
		r.Post("/refresh",          authH.RefreshToken)
		r.Post("/logout",           authH.Logout)
		r.Post("/forgot-password",  authH.ForgotPassword)
		r.Post("/reset-password",   authH.ResetPassword)
		r.Post("/verify-email",     authH.VerifyEmail)

		// Google OAuth2 — public (no JWT needed, callback exchanges code for JWT)
		r.Get("/google",            googleH.Initiate)
		r.Get("/google/callback",   googleH.Callback)

		// Protected auth sub-routes
		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtMgr))
			r.Get("/me",                 authH.Me)
			r.Put("/me",                 authH.UpdateProfile)
			r.Post("/me/photo",          photoH.UploadPhoto)
			r.Post("/logout-all",        authH.LogoutAll)
			r.Post("/change-password",   authH.ChangePassword)
			r.Post("/send-verification", authH.SendVerificationEmail)
		})
	})

	// ══════════════════════════════════════════════════════════════════════════
	// API KEYS — authenticated user manages their own keys
	// ══════════════════════════════════════════════════════════════════════════
	r.Route("/api-keys", func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtMgr))
		r.Post("/",       apiKeyH.CreateAPIKey)
		r.Get("/",        apiKeyH.ListAPIKeys)
		r.Delete("/{id}", apiKeyH.RevokeAPIKey)
	})

	// ══════════════════════════════════════════════════════════════════════════
	// USERS — tenant-scoped user management (Tenant Admin+)
	// ══════════════════════════════════════════════════════════════════════════
	r.Route("/users", func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtMgr))
		r.Use(middleware.RequireRole(
			domain.RoleTenantAdmin,
			domain.RolePlatformOperator,
		))
		r.Get("/",              userH.ListUsers)
		r.Get("/{id}",          userH.GetUser)
		r.Put("/{id}/role",     userH.UpdateUserRole)
		r.Post("/{id}/suspend", userH.SuspendUser)
		r.Post("/{id}/activate",userH.ActivateUser)
		r.Delete("/{id}",       userH.DeleteUser)
	})

	// ══════════════════════════════════════════════════════════════════════════
	// TENANTS — platform operator only
	// ══════════════════════════════════════════════════════════════════════════
	r.Route("/tenants", func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtMgr))
		r.Use(middleware.RequireRole(domain.RolePlatformOperator))
		r.Post("/",    tenantH.CreateTenant)
		r.Get("/",     tenantH.ListTenants)
		r.Get("/{id}", tenantH.GetTenant)
	})

	return r
}
