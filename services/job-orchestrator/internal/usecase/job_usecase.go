package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/ethioscript/job-orchestrator/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Config holds job orchestrator policy settings.
type Config struct {
	MaxRetryAttempts        int
	RetryBackoffBaseSeconds int
	OutboxPollInterval      time.Duration
	JobHeartbeatSeconds     int
	JobTimeoutSeconds       int
	LeaseDurationSeconds    int
	OCRQueue                string
	STTQueue                string
	DeadLetterQueue         string
	Exchange                string
}

// JobUseCase contains all job orchestration business operations.
type JobUseCase struct {
	db     *pgxpool.Pool
	amqpCh *amqp.Channel
	redis  *redis.Client
	cfg    Config
	log    *zap.Logger
}

// NewJobUseCase constructs the use case.
func NewJobUseCase(db *pgxpool.Pool, ch *amqp.Channel, rdb *redis.Client, cfg Config, log *zap.Logger) *JobUseCase {
	return &JobUseCase{db: db, amqpCh: ch, redis: rdb, cfg: cfg, log: log}
}

// GetJob retrieves a job by ID, enforcing tenant isolation.
func (uc *JobUseCase) GetJob(ctx context.Context, tenantID, jobID uuid.UUID) (*domain.Job, error) {
	var j domain.Job
	err := uc.db.QueryRow(ctx,
		`SELECT id, document_id, tenant_id, task_type, state, attempts, max_attempts,
		        worker_id, lease_expires_at, model_name, model_version, language,
		        error_code, error_message, retry_after, priority, started_at, finished_at,
		        created_at, updated_at
		 FROM processing_jobs
		 WHERE id = $1 AND tenant_id = $2`,
		jobID, tenantID,
	).Scan(
		&j.ID, &j.DocumentID, &j.TenantID, &j.TaskType, &j.State,
		&j.Attempts, &j.MaxAttempts, &j.WorkerID, &j.LeaseExpiresAt,
		&j.ModelName, &j.ModelVersion, &j.Language,
		&j.ErrorCode, &j.ErrorMessage, &j.RetryAfter,
		&j.Priority, &j.StartedAt, &j.FinishedAt,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, domain.ErrJobNotFound
	}
	return &j, nil
}

// CancelJob cancels a job that is in a cancellable state.
func (uc *JobUseCase) CancelJob(ctx context.Context, tenantID, jobID uuid.UUID) error {
	job, err := uc.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return err
	}
	if !job.CanTransitionTo(domain.StateCancelled) {
		return domain.ErrJobNotCancellable
	}

	_, err = uc.db.Exec(ctx,
		`UPDATE processing_jobs SET state = 'CANCELLED', updated_at = NOW() WHERE id = $1`,
		jobID,
	)
	return err
}

// HandleWorkerResult processes a result published by a worker via RabbitMQ.
// This is the idempotent result handler — safe to call multiple times.
func (uc *JobUseCase) HandleWorkerResult(ctx context.Context, result domain.JobResult) error {
	jobID, err := uuid.Parse(result.JobID)
	if err != nil {
		return fmt.Errorf("invalid job_id: %w", err)
	}
	tenantID, err := uuid.Parse(result.TenantID)
	if err != nil {
		return fmt.Errorf("invalid tenant_id: %w", err)
	}
	docID, err := uuid.Parse(result.DocumentID)
	if err != nil {
		return fmt.Errorf("invalid document_id: %w", err)
	}

	job, err := uc.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return err
	}

	// Idempotency: if job is already terminal, skip
	if job.State == domain.StateCompleted || job.State == domain.StateCancelled {
		uc.log.Info("result received for already-terminal job, skipping",
			zap.String("job_id", jobID.String()),
			zap.String("state", string(job.State)),
		)
		return nil
	}

	now := time.Now().UTC()
	tx, txErr := uc.db.Begin(ctx)
	if txErr != nil {
		return txErr
	}
	defer func() {
		if txErr != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if result.Success {
		// Store result version
		modelMeta, _ := json.Marshal(map[string]string{
			"model_name":    result.ModelName,
			"model_version": result.ModelVersion,
			"language":      result.Language,
		})
		_, txErr = tx.Exec(ctx,
			`INSERT INTO result_versions
			   (id, document_id, tenant_id, job_id, version, is_approved, raw_text_key,
			    confidence_score, model_metadata, page_count, word_count, char_count, created_by, created_at, updated_at)
			 VALUES
			   ($1, $2, $3, $4,
			    COALESCE((SELECT MAX(version) FROM result_versions WHERE document_id = $2), 0) + 1,
			    FALSE, $5, $6, $7, $8, $9, $10,
			    (SELECT created_by FROM documents WHERE id = $2),
			    $11, $12)`,
			uuid.New(), docID, tenantID, jobID,
			nullString(result.RawTextKey), result.Confidence, modelMeta,
			nullInt(result.PageCount), nullInt(result.WordCount), nullInt(result.CharCount),
			now, now,
		)
		if txErr != nil {
			return fmt.Errorf("insert result version: %w", txErr)
		}

		// Transition job and document to REVIEW_REQUIRED
		_, txErr = tx.Exec(ctx,
			`UPDATE processing_jobs
			 SET state = 'REVIEW_REQUIRED', finished_at = $1, model_name = $2, model_version = $3, updated_at = $1
			 WHERE id = $4`,
			now, result.ModelName, result.ModelVersion, jobID,
		)
		if txErr != nil {
			return fmt.Errorf("update job: %w", txErr)
		}
		_, txErr = tx.Exec(ctx,
			`UPDATE documents SET status = 'REVIEW_REQUIRED', updated_at = $1 WHERE id = $2`,
			now, docID,
		)
		if txErr != nil {
			return fmt.Errorf("update document: %w", txErr)
		}

	} else {
		// Failure path
		if job.IsRetryable() {
			backoff := time.Duration(math.Pow(float64(uc.cfg.RetryBackoffBaseSeconds), float64(job.Attempts+1))) * time.Second
			retryAt := now.Add(backoff)
			_, txErr = tx.Exec(ctx,
				`UPDATE processing_jobs
				 SET state = 'RETRYING', attempts = attempts + 1, retry_after = $1,
				     error_code = $2, error_message = $3, updated_at = $4
				 WHERE id = $5`,
				retryAt, result.ErrorCode, result.ErrorMessage, now, jobID,
			)
		} else {
			// Dead-letter
			_, txErr = tx.Exec(ctx,
				`UPDATE processing_jobs
				 SET state = 'FAILED', finished_at = $1, attempts = attempts + 1,
				     error_code = $2, error_message = $3, updated_at = $1
				 WHERE id = $4`,
				now, result.ErrorCode, result.ErrorMessage, jobID,
			)
			if txErr == nil {
				_, txErr = tx.Exec(ctx,
					`UPDATE documents SET status = 'FAILED', updated_at = $1 WHERE id = $2`,
					now, docID,
				)
			}
			// Publish to dead-letter queue
			uc.publishDeadLetter(result)
		}
		if txErr != nil {
			return fmt.Errorf("update job on failure: %w", txErr)
		}
	}

	txErr = tx.Commit(ctx)
	if txErr != nil {
		return txErr
	}

	// Write audit event and usage record after successful commit
	go func() {
		bgCtx := context.Background()
		action := "JOB_COMPLETED"
		if !result.Success {
			action = "JOB_FAILED"
		}
		uc.WriteAuditEvent(bgCtx, tenantID, action, "ProcessingJob", jobID.String(), result.ErrorMessage)
		if result.Success {
			unitType := "PAGE"
			quantity := float64(result.PageCount)
			if result.PageCount == 0 {
				// STT jobs — use word count as proxy unit
				unitType = "INFERENCE_UNIT"
				quantity = 1
			}
			uc.WriteUsageRecord(bgCtx, tenantID, jobID, docID, unitType, quantity)
		}
	}()

	return nil
}

// RunOutboxPublisher polls the outbox table and publishes unpublished events to the broker.
// This should be run in a separate goroutine.
func (uc *JobUseCase) RunOutboxPublisher(ctx context.Context) {
	ticker := time.NewTicker(uc.cfg.OutboxPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			uc.log.Info("outbox publisher stopping")
			return
		case <-ticker.C:
			uc.publishPendingOutboxEvents(ctx)
		}
	}
}

func (uc *JobUseCase) publishPendingOutboxEvents(ctx context.Context) {
	rows, err := uc.db.Query(ctx,
		`SELECT id, aggregate_id, event_type, tenant_id, payload
		 FROM outbox_events
		 WHERE published = FALSE
		 ORDER BY created_at ASC
		 LIMIT 50
		 FOR UPDATE SKIP LOCKED`,
	)
	if err != nil {
		uc.log.Error("outbox query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, aggregateID uuid.UUID
			eventType, tenantID string
			payload        []byte
		)
		if err := rows.Scan(&id, &aggregateID, &eventType, &tenantID, &payload); err != nil {
			uc.log.Error("outbox scan failed", zap.Error(err))
			continue
		}

		// Determine routing key from event type + payload task_type
		routingKey := eventTypeToRoutingKey(eventType, payload)
		err := uc.amqpCh.Publish(
			uc.cfg.Exchange,
			routingKey,
			false, false,
			amqp.Publishing{
				ContentType:  "application/json",
				Body:         payload,
				DeliveryMode: amqp.Persistent,
				MessageId:    id.String(),
				Headers: amqp.Table{
					"tenant_id":    tenantID,
					"aggregate_id": aggregateID.String(),
					"event_type":   eventType,
				},
			},
		)
		if err != nil {
			uc.log.Error("publish failed", zap.Error(err), zap.String("event_id", id.String()))
			_, _ = uc.db.Exec(ctx,
				`UPDATE outbox_events SET publish_attempts = publish_attempts + 1, error = $1 WHERE id = $2`,
				err.Error(), id,
			)
			continue
		}

		_, _ = uc.db.Exec(ctx,
			`UPDATE outbox_events SET published = TRUE, published_at = NOW() WHERE id = $1`,
			id,
		)
	}
}

// RunLeaseReaper periodically finds stalled jobs and marks them for retry.
func (uc *JobUseCase) RunLeaseReaper(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := uc.db.Exec(ctx,
				`UPDATE processing_jobs
				 SET state = 'RETRYING', worker_id = NULL, lease_expires_at = NULL, updated_at = NOW()
				 WHERE state = 'PROCESSING'
				   AND lease_expires_at < NOW()
				   AND attempts < max_attempts`,
			)
			if err != nil {
				uc.log.Error("lease reaper failed", zap.Error(err))
			}
		}
	}
}

// WriteAuditEvent writes an audit event to the audit_events table.
func (uc *JobUseCase) WriteAuditEvent(ctx context.Context, tenantID uuid.UUID, action, resourceType, resourceID, errorMsg string) {
	payload, _ := json.Marshal(map[string]string{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"error":         errorMsg,
	})
	_, err := uc.db.Exec(ctx,
		`INSERT INTO audit_events (id, tenant_id, action, resource_type, resource_id, metadata, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		uuid.New(), tenantID, action, resourceType, resourceID, payload,
	)
	if err != nil {
		uc.log.Warn("failed to write audit event", zap.Error(err), zap.String("action", action))
	}
}

// WriteUsageRecord writes a usage record for billing/metering.
func (uc *JobUseCase) WriteUsageRecord(ctx context.Context, tenantID, jobID, docID uuid.UUID, unitType string, quantity float64) {
	now := time.Now().UTC()
	periodMonth := now.Format("2006-01")
	_, err := uc.db.Exec(ctx,
		`INSERT INTO usage_records (id, tenant_id, job_id, document_id, unit_type, quantity, period_month, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT DO NOTHING`,
		uuid.New(), tenantID, jobID, docID, unitType, quantity, periodMonth, now,
	)
	if err != nil {
		uc.log.Warn("failed to write usage record", zap.Error(err))
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (uc *JobUseCase) publishDeadLetter(result domain.JobResult) {
	payload, _ := json.Marshal(result)
	_ = uc.amqpCh.Publish(
		uc.cfg.Exchange,
		"jobs.dead-letter",
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         payload,
			DeliveryMode: amqp.Persistent,
		},
	)
}

func eventTypeToRoutingKey(eventType string, payload []byte) string {
	switch eventType {
	case "document.processing.requested":
		// Inspect task_type in the payload to route OCR vs STT correctly
		var p struct {
			TaskType string `json:"task_type"`
		}
		if err := json.Unmarshal(payload, &p); err == nil && p.TaskType == "STT" {
			return "jobs.stt"
		}
		return "jobs.ocr"
	default:
		return eventType
	}
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int) interface{} {
	if i == 0 {
		return nil
	}
	return i
}
