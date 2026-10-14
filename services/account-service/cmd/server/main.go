package main

// @title           EthioScript — Account Service
// @version         3.1.0
// @description     Full account management: registration, login, JWT auth, refresh tokens, email verification, password reset, roles, tenants, and API keys.
// @contact.name    EthioScript Team
// @contact.email   api@ethioscript.io
// @license.name    Proprietary
// @host            localhost:6000
// @BasePath        /
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     Type "Bearer" followed by a space and the JWT access token.
// @schemes         http https

import (
	"context"
	"embed"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethioscript/account-service/internal/auth"
	"github.com/ethioscript/account-service/internal/config"
	"github.com/ethioscript/account-service/internal/handler"
	"github.com/ethioscript/account-service/internal/repository"
	"github.com/ethioscript/account-service/internal/usecase"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

//go:embed docs/openapi.yml
var openapiFS embed.FS

func main() {
	// ── Logger ────────────────────────────────────────────────────────────────
	log, _ := zap.NewProduction()
	defer log.Sync()

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}
	log.Info("starting EthioScript Account Service",
		zap.String("port", cfg.Server.Port),
		zap.String("env", cfg.Server.Env),
	)

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dbCancel()

	db, err := pgxpool.New(dbCtx, cfg.DB.DSN)
	if err != nil {
		log.Fatal("postgres init failed", zap.Error(err))
	}
	defer db.Close()

	if err := db.Ping(dbCtx); err != nil {
		log.Fatal("postgres ping failed", zap.Error(err))
	}
	log.Info("postgres connected")

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	redisCtx, redisCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer redisCancel()
	if err := rdb.Ping(redisCtx).Err(); err != nil {
		log.Fatal("redis ping failed", zap.Error(err))
	}
	log.Info("redis connected")

	// ── Dependencies ──────────────────────────────────────────────────────────
	jwtMgr := auth.NewManager(
		cfg.Auth.JWTSecret,
		cfg.Auth.JWTExpiryHours,
		cfg.Auth.RefreshTokenExpiryDays,
	)
	repo := repository.NewAccountRepo(db)
	uc   := usecase.NewAccountUseCase(repo, jwtMgr, cfg, log)

	// ── Router ────────────────────────────────────────────────────────────────
	router := handler.NewRouter(cfg, db, rdb, jwtMgr, uc, log, openapiFS)

	// ── HTTP Server ───────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("account-service listening", zap.String("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gracefully…")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown error", zap.Error(err))
	}
	log.Info("account-service stopped")
}
