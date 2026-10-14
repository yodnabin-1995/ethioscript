package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethioscript/document-service/internal/handler"
	"github.com/ethioscript/document-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

//go:embed docs/openapi.yml
var openapiSpec embed.FS

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	port := getEnv("PORT", "6001")
	dsn := mustGetEnv("POSTGRES_DSN")
	rabbitURL := mustGetEnv("RABBITMQ_URL")
	exchange := getEnv("RABBITMQ_EXCHANGE", "ethioscript.events")

	// ── PostgreSQL ──────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := pgxpool.New(ctx, dsn)
	if err != nil || db.Ping(ctx) != nil {
		log.Fatal("postgres connection failed", zap.Error(err))
	}
	log.Info("postgres connected")

	// ── RabbitMQ ─────────────────────────────────────────────────────────────
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		log.Fatal("rabbitmq connection failed", zap.Error(err))
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatal("rabbitmq channel failed", zap.Error(err))
	}
	defer ch.Close()

	// Declare topic exchange
	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		log.Fatal("exchange declare failed", zap.Error(err))
	}
	log.Info("rabbitmq connected", zap.String("exchange", exchange))

	// ── Use case & Handler ────────────────────────────────────────────────────
	uc := usecase.NewDocumentUseCase(db, ch, exchange, log)
	docH := handler.NewDocumentHandler(uc, log)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.StripSlashes)

	r.Get("/health", docH.Health)
	r.Handle("/metrics", promhttp.Handler())

	// ── Swagger UI (embedded spec) ────────────────────────────────────────────
	r.Get("/docs/openapi.yml", func(w http.ResponseWriter, r *http.Request) {
		data, _ := openapiSpec.ReadFile("docs/openapi.yml")
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(data)
	})
	r.Get("/docs", http.RedirectHandler("/docs/index.html", http.StatusMovedPermanently).ServeHTTP)
	r.Get("/docs/*", httpSwagger.Handler(
		httpSwagger.URL("/docs/openapi.yml"),
		httpSwagger.DeepLinking(true),
		httpSwagger.DocExpansion("list"),
	))

	// Clean routes — no /api/v1 prefix
	r.Post("/documents", docH.CreateDocument)
	r.Get("/documents", docH.ListDocuments)
	r.Get("/documents/{id}", docH.GetDocument)
	r.Delete("/documents/{id}", docH.DeleteDocument)
	r.Put("/documents/{id}/result/draft", docH.SaveDraft)
	r.Post("/documents/{id}/approve", docH.ApproveDocument)

	// ── Server ────────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		log.Info("document-service listening", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("document-service stopped")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %q is not set", key))
	}
	return v
}
