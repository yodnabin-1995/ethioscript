package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ethioscript/job-orchestrator/internal/domain"
	"github.com/ethioscript/job-orchestrator/internal/usecase"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

// openapiSpecPath is read at request time — update the file and restart the
// container to see changes in Swagger UI, no image rebuild required.
const openapiSpecPath = "/app/docs/openapi.yml"

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	port := getEnv("PORT", "6002")
	dsn := mustGetEnv("POSTGRES_DSN")
	rabbitURL := mustGetEnv("RABBITMQ_URL")
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPass := getEnv("REDIS_PASSWORD", "")

	internalKey := getEnv("INTERNAL_API_KEY", "")

	maxRetry, _ := strconv.Atoi(getEnv("MAX_RETRY_ATTEMPTS", "3"))
	backoffBase, _ := strconv.Atoi(getEnv("RETRY_BACKOFF_BASE_SECONDS", "30"))
	outboxPoll, _ := strconv.Atoi(getEnv("OUTBOX_POLL_INTERVAL_SECONDS", "5"))
	jobTimeout, _ := strconv.Atoi(getEnv("JOB_TIMEOUT_SECONDS", "300"))
	leaseDuration, _ := strconv.Atoi(getEnv("LEASE_DURATION_SECONDS", "60"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── PostgreSQL ─────────────────────────────────────────────────────────
	dbCtx, dbCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dbCancel()
	db, err := pgxpool.New(dbCtx, dsn)
	if err != nil || db.Ping(dbCtx) != nil {
		log.Fatal("postgres connection failed", zap.Error(err))
	}
	log.Info("postgres connected")

	// ── Redis ──────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass, DB: 1})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis ping failed", zap.Error(err))
	}
	log.Info("redis connected")

	// ── RabbitMQ ──────────────────────────────────────────────────────────
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

	exchange := getEnv("RABBITMQ_EXCHANGE", "ethioscript.events")
	resultsQueue := getEnv("RABBITMQ_QUEUE_RESULTS", "ethioscript.jobs.results")
	deadLetterQueue := getEnv("RABBITMQ_QUEUE_DEAD_LETTER", "ethioscript.jobs.dead-letter")

	_ = ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)

	// Declare result, dead-letter AND worker queues with correct bindings
	workerQueues := map[string]string{
		getEnv("RABBITMQ_QUEUE_OCR", "ethioscript.jobs.ocr"):                "jobs.ocr",
		getEnv("RABBITMQ_QUEUE_STT", "ethioscript.jobs.stt"):                "jobs.stt",
		resultsQueue:    "jobs.results",
		deadLetterQueue: "jobs.dead-letter",
	}
	for qName, routingKey := range workerQueues {
		if _, err := ch.QueueDeclare(qName, true, false, false, false, nil); err != nil {
			log.Fatal("queue declare failed", zap.String("queue", qName), zap.Error(err))
		}
		if err := ch.QueueBind(qName, routingKey, exchange, false, nil); err != nil {
			log.Warn("queue bind failed", zap.String("queue", qName), zap.String("key", routingKey), zap.Error(err))
		}
	}
	log.Info("rabbitmq connected", zap.String("exchange", exchange))

	// ── Use case ──────────────────────────────────────────────────────────
	jobUC := usecase.NewJobUseCase(db, ch, rdb, usecase.Config{
		MaxRetryAttempts:        maxRetry,
		RetryBackoffBaseSeconds: backoffBase,
		OutboxPollInterval:      time.Duration(outboxPoll) * time.Second,
		JobTimeoutSeconds:       jobTimeout,
		LeaseDurationSeconds:    leaseDuration,
		Exchange:                exchange,
		DeadLetterQueue:         deadLetterQueue,
	}, log)

	// ── Background workers ────────────────────────────────────────────────
	go jobUC.RunOutboxPublisher(ctx)
	go jobUC.RunLeaseReaper(ctx)

	// ── Result consumer ───────────────────────────────────────────────────
	go consumeResults(ctx, ch, resultsQueue, jobUC, log)

	allowedOrigins := strings.Split(getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8000"), ",")

	// ── HTTP router ───────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Tenant-ID", "X-Internal-Key", "X-Trace-ID"},
		ExposedHeaders:   []string{"X-Trace-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	// Structured request logger — logs every request with method, path, status, duration, trace_id
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			traceID := req.Header.Get("X-Trace-ID")
			if traceID == "" {
				traceID = uuid.NewString()
			}
			w.Header().Set("X-Trace-ID", traceID)
			start := time.Now()
			wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(wrapped, req)
			lvl := log.Info
			if wrapped.status >= 500 {
				lvl = log.Error
			} else if wrapped.status >= 400 {
				lvl = log.Warn
			}
			lvl("request",
				zap.String("method",     req.Method),
				zap.String("path",       req.URL.Path),
				zap.Int("status",        wrapped.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("trace_id",   traceID),
				zap.String("ip",         req.RemoteAddr),
			)
		})
	})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"job-orchestrator"}`))
	})
	r.Handle("/metrics", promhttp.Handler())

	// ── Swagger UI — spec read from disk, no rebuild needed on spec changes ──
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
	swaggerHandlerJob := httpSwagger.Handler(
		httpSwagger.URL("/docs/openapi.yml?v=2"),
		httpSwagger.DeepLinking(true),
		httpSwagger.DocExpansion("list"),
	)
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/docs/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusFound)
	})
	r.Get("/docs/*", swaggerHandlerJob)

	// Clean routes — protected by X-Internal-Key
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if internalKey != "" && req.Header.Get("X-Internal-Key") != internalKey {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":"forbidden","code":"FORBIDDEN"}`))
					return
				}
				next.ServeHTTP(w, req)
			})
		})

		r.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
			tenantIDStr := r.Header.Get("X-Tenant-ID")
			tenantID, err := uuid.Parse(tenantIDStr)
			if err != nil {
				log.Warn("invalid tenant id", zap.String("value", tenantIDStr), zap.String("path", r.URL.Path))
				http.Error(w, `{"error":"invalid tenant"}`, http.StatusUnauthorized)
				return
			}
			jobID, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				http.Error(w, `{"error":"invalid job id"}`, http.StatusBadRequest)
				return
			}
			job, err := jobUC.GetJob(r.Context(), tenantID, jobID)
			if errors.Is(err, domain.ErrJobNotFound) {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("get job failed",
					zap.Error(err),
					zap.String("job_id", jobID.String()),
					zap.String("tenant_id", tenantID.String()),
				)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			_ = enc.Encode(job)
		})

		r.Post("/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
			tenantID, _ := uuid.Parse(r.Header.Get("X-Tenant-ID"))
			jobID, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				http.Error(w, `{"error":"invalid job id"}`, http.StatusBadRequest)
				return
			}
			if err := jobUC.CancelJob(r.Context(), tenantID, jobID); err != nil {
				log.Warn("cancel job failed",
					zap.Error(err),
					zap.String("job_id", jobID.String()),
				)
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}) // end internal group

	// ── Server ────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		log.Info("job-orchestrator listening", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("job-orchestrator stopped")
}

// consumeResults listens on the results queue and handles worker outcomes.
func consumeResults(ctx context.Context, ch *amqp.Channel, queue string, uc *usecase.JobUseCase, log *zap.Logger) {
	msgs, err := ch.Consume(queue, "job-orchestrator", false, false, false, false, nil)
	if err != nil {
		log.Error("consume failed", zap.Error(err))
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			var result domain.JobResult
			if err := json.Unmarshal(msg.Body, &result); err != nil {
				log.Error("unmarshal result failed", zap.Error(err))
				_ = msg.Nack(false, false) // dead-letter
				continue
			}
			if err := uc.HandleWorkerResult(ctx, result); err != nil {
				log.Error("handle result failed", zap.Error(err), zap.String("job_id", result.JobID))
				_ = msg.Nack(false, false)
				continue
			}
			_ = msg.Ack(false)
		}
	}
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

// statusWriter wraps ResponseWriter to capture the status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
