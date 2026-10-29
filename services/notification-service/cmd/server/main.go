package main

// @title           EthioScript — Notification Service
// @version         1.0.0
// @description     Standalone notification microservice for the EthioScript platform.
// @description
// @description     ## Features
// @description     - In-app inbox (paginated, unread count, mark-read)
// @description     - Web Push / VAPID subscription management
// @description     - OTP send & verify
// @description     - Email / SMS / Telegram channel stubs
// @description     - Bulk broadcast
// @description     - RabbitMQ consumer (jobs.results, document.processing.requested)
// @description     - Transactional outbox worker
// @description     - Prometheus metrics
// @description
// @description     ## Authentication
// @description     - **User endpoints** — `Authorization: Bearer <JWT>` from account-service login
// @description     - **Internal endpoints** — `X-Internal-Key: <INTERNAL_API_KEY>` header
// @description     - **Admin endpoints** — Bearer JWT with `role=ADMIN`
//
// @contact.name   EthioScript Team
// @contact.email  api@ethioscript.io
// @license.name   Proprietary
//
// @host            localhost:6007
// @BasePath        /
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     JWT from account-service. Format: `Bearer <token>`
//
// @securityDefinitions.apikey InternalKey
// @in              header
// @name            X-Internal-Key
// @description     Shared internal service key — value of INTERNAL_API_KEY in .env
//
// @schemes         http https

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethioscript/notification-service/internal/config"
	"github.com/ethioscript/notification-service/internal/consumer"
	"github.com/ethioscript/notification-service/internal/handler"
	"github.com/ethioscript/notification-service/internal/repository"
	"github.com/ethioscript/notification-service/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func main() {
	// ── Logger ────────────────────────────────────────────────────────────────
	log, _ := zap.NewProduction()
	defer log.Sync() //nolint:errcheck

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}
	log.Info("starting EthioScript Notification Service",
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

	// ── Repository ────────────────────────────────────────────────────────────
	repo := repository.New(db)

	// ── Router ────────────────────────────────────────────────────────────────
	router := handler.NewRouter(cfg, repo, log)

	// ── HTTP Server ───────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:           ":" + cfg.Server.Port,
		Handler:        router,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// ── Background workers ────────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Outbox worker — polls DB and dispatches pending notifications
	outboxW := worker.NewOutboxWorker(repo, cfg.OutboxPoll(), log)
	go outboxW.Run(ctx)

	// RabbitMQ consumer — listens for job/document events
	mqConsumer := consumer.New(cfg.RabbitMQ.URL, repo, log)
	go mqConsumer.Run(ctx)

	// ── Start HTTP server ─────────────────────────────────────────────────────
	go func() {
		log.Info("notification-service listening", zap.String("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gracefully…")
	cancel() // stop background workers

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown error", zap.Error(err))
	}
	log.Info("notification-service stopped")
}
