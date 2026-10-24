package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ethioscript/document-service/internal/adapter/storage"
	"github.com/ethioscript/document-service/internal/handler"
	"github.com/ethioscript/document-service/internal/middleware"
	"github.com/ethioscript/document-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

// openapiSpecPath is read at request time — update the file and restart the
// container to see changes in Swagger UI, no image rebuild required.
const openapiSpecPath = "/app/docs/openapi.yml"

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	port        := getEnv("PORT", "6001")
	dsn         := mustGetEnv("POSTGRES_DSN")
	rabbitURL   := mustGetEnv("RABBITMQ_URL")
	exchange    := getEnv("RABBITMQ_EXCHANGE", "ethioscript.events")
	internalKey := getEnv("INTERNAL_API_KEY", "")

	// MinIO config
	minioEndpoint  := getEnv("MINIO_ENDPOINT", "minio:9000")
	minioAccessKey := getEnv("MINIO_ACCESS_KEY", "minioadmin")
	minioSecretKey := getEnv("MINIO_SECRET_KEY", "minioadmin123")
	minioBucket    := getEnv("MINIO_BUCKET", "ethioscript-assets")
	minioSSL, _    := strconv.ParseBool(getEnv("MINIO_USE_SSL", "false"))
	maxUploadMB, _ := strconv.ParseInt(getEnv("MAX_UPLOAD_SIZE_MB", "50"), 10, 64)

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

	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		log.Fatal("exchange declare failed", zap.Error(err))
	}
	log.Info("rabbitmq connected", zap.String("exchange", exchange))

	// ── MinIO ─────────────────────────────────────────────────────────────────
	minioClient, err := storage.New(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket, minioSSL)
	if err != nil {
		log.Fatal("minio client failed", zap.Error(err))
	}
	log.Info("minio connected", zap.String("bucket", minioBucket))

	// ── Use case & Handlers ───────────────────────────────────────────────────
	uc     := usecase.NewDocumentUseCase(db, ch, exchange, log)
	docH   := handler.NewDocumentHandler(uc, minioClient, log)
	assetH := handler.NewAssetHandler(uc, minioClient, minioBucket, log)

	_ = maxUploadMB // used for future validation middleware

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)

	// Public system endpoints (no auth)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/health", docH.Health)
	r.Handle("/metrics", promhttp.Handler())

	// Swagger UI — spec read from disk, no rebuild needed on spec changes
	r.Get("/docs/openapi.yml", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(openapiSpecPath)
		if err != nil {
			http.Error(w, "openapi spec not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	})
	swaggerHandler := httpSwagger.Handler(
		httpSwagger.URL("/docs/openapi.yml?v=2"),
		httpSwagger.DeepLinking(true),
		httpSwagger.DocExpansion("list"),
	)
	r.Get("/docs", swaggerHandler)
	r.Get("/docs/", swaggerHandler)
	r.Get("/docs/*", swaggerHandler)

	// ── Internal API — protected by X-Internal-Key ────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(middleware.InternalOnly(internalKey))

		// Asset upload
		r.Post("/assets", assetH.InitUpload)

		// Document CRUD
		r.Post("/documents", docH.CreateDocument)
		r.Get("/documents", docH.ListDocumentsFiltered)
		r.Get("/documents/{id}", docH.GetDocument)
		r.Delete("/documents/{id}", docH.DeleteDocument)

		// Result lifecycle
		r.Get("/documents/{id}/result", docH.GetResult)
		r.Put("/documents/{id}/result/draft", docH.SaveDraft)
		r.Post("/documents/{id}/approve", docH.ApproveDocument)
		r.Post("/documents/{id}/retry", docH.RetryDocument)
		r.Post("/documents/{id}/exports", docH.ExportDocument)
	})

	// ── Server ────────────────────────────────────────────────────────────────
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

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
	srvCancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	_ = srvCtx
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
