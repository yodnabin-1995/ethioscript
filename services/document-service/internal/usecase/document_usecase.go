package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ethioscript/document-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// DocumentUseCase contains all document business operations.
type DocumentUseCase struct {
	db       *pgxpool.Pool
	amqpCh   *amqp.Channel
	exchange string
	log      *zap.Logger
}

// NewDocumentUseCase constructs the use case.
func NewDocumentUseCase(db *pgxpool.Pool, ch *amqp.Channel, exchange string, log *zap.Logger) *DocumentUseCase {
	return &DocumentUseCase{db: db, amqpCh: ch, exchange: exchange, log: log}
}

// CreateDocument creates a document record, a processing job, and an outbox event atomically.
func (uc *DocumentUseCase) CreateDocument(ctx context.Context, tenantID, userID uuid.UUID, req domain.CreateDocumentRequest) (*domain.Document, error) {
	assetID, err := uuid.Parse(req.AssetID)
	if err != nil {
		return nil, fmt.Errorf("invalid asset_id: %w", err)
	}

	// Verify asset belongs to tenant
	var assetExists bool
	err = uc.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM assets WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		assetID, tenantID,
	).Scan(&assetExists)
	if err != nil || !assetExists {
		return nil, fmt.Errorf("asset not found or not accessible")
	}

	lang := req.Language
	if lang == "" {
		lang = "amh"
	}
	taskType := req.TaskType
	if taskType == "" {
		taskType = domain.TaskOCR
	}
	// tags is NOT NULL in DB — default to empty slice when not provided
	if req.Tags == nil {
		req.Tags = []string{}
	}

	docID := uuid.New()
	jobID := uuid.New()
	outboxID := uuid.New()
	now := time.Now().UTC()

	// Outbox event payload
	eventPayload, _ := json.Marshal(map[string]any{
		"document_id": docID.String(),
		"job_id":      jobID.String(),
		"asset_id":    assetID.String(),
		"tenant_id":   tenantID.String(),
		"task_type":   taskType,
		"language":    lang,
	})

	// Single atomic transaction: document + job + outbox event
	tx, err := uc.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Insert document
	_, err = tx.Exec(ctx,
		`INSERT INTO documents (id, tenant_id, asset_id, filename, language, task_type, status, title, description, tags, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 'UPLOADED', $7, $8, $9, $10, $11, $12)`,
		docID, tenantID, assetID, req.Filename, lang, taskType,
		req.Title, req.Description, req.Tags, userID, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	// Insert processing job
	_, err = tx.Exec(ctx,
		`INSERT INTO processing_jobs (id, document_id, tenant_id, task_type, state, language, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'PENDING', $5, $6, $7)`,
		jobID, docID, tenantID, taskType, lang, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}

	// Insert outbox event
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events (id, aggregate_id, aggregate_type, event_type, tenant_id, payload, occurred_at)
		 VALUES ($1, $2, 'Document', 'document.processing.requested', $3, $4, $5)`,
		outboxID, docID, tenantID, eventPayload, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert outbox event: %w", err)
	}

	// Update document status to QUEUED
	_, err = tx.Exec(ctx,
		`UPDATE documents SET status = 'QUEUED', updated_at = $1 WHERE id = $2`,
		now, docID,
	)
	if err != nil {
		return nil, fmt.Errorf("update document status: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	uc.log.Info("document created",
		zap.String("document_id", docID.String()),
		zap.String("job_id", jobID.String()),
		zap.String("task_type", string(taskType)),
	)

	return &domain.Document{
		ID:        docID,
		TenantID:  tenantID,
		AssetID:   assetID,
		Filename:  req.Filename,
		Language:  lang,
		TaskType:  taskType,
		Status:    domain.StatusQueued,
		Tags:      req.Tags,
		Title:     req.Title,
		CreatedBy: userID,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetDocument fetches a document, enforcing tenant isolation.
func (uc *DocumentUseCase) GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (*domain.Document, error) {
	var doc domain.Document
	err := uc.db.QueryRow(ctx,
		`SELECT id, tenant_id, asset_id, filename, language, task_type, status,
		        title, description, tags, page_count, created_by, assigned_to,
		        approved_by, approved_at, created_at, updated_at
		 FROM documents
		 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		docID, tenantID,
	).Scan(
		&doc.ID, &doc.TenantID, &doc.AssetID, &doc.Filename, &doc.Language,
		&doc.TaskType, &doc.Status, &doc.Title, &doc.Description, &doc.Tags,
		&doc.PageCount, &doc.CreatedBy, &doc.AssignedTo,
		&doc.ApprovedBy, &doc.ApprovedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)
	if err != nil {
		return nil, domain.ErrDocumentNotFound
	}
	return &doc, nil
}

// ListDocuments returns a paginated list scoped to a tenant.
func (uc *DocumentUseCase) ListDocuments(ctx context.Context, params domain.ListDocumentsParams) (*domain.DocumentListResponse, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}
	offset := (params.Page - 1) * params.PageSize

	rows, err := uc.db.Query(ctx,
		`SELECT id, tenant_id, asset_id, filename, language, task_type, status,
		        title, tags, page_count, created_by, created_at, updated_at
		 FROM documents
		 WHERE tenant_id = $1 AND deleted_at IS NULL
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		params.TenantID, params.PageSize, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []domain.Document
	for rows.Next() {
		var doc domain.Document
		if err := rows.Scan(
			&doc.ID, &doc.TenantID, &doc.AssetID, &doc.Filename, &doc.Language,
			&doc.TaskType, &doc.Status, &doc.Title, &doc.Tags,
			&doc.PageCount, &doc.CreatedBy, &doc.CreatedAt, &doc.UpdatedAt,
		); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	var total int
	_ = uc.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM documents WHERE tenant_id = $1 AND deleted_at IS NULL`,
		params.TenantID,
	).Scan(&total)

	totalPages := (total + params.PageSize - 1) / params.PageSize

	return &domain.DocumentListResponse{
		Documents:  docs,
		Total:      total,
		Page:       params.Page,
		PageSize:   params.PageSize,
		TotalPages: totalPages,
	}, nil
}

// SaveDraft saves an editable draft for the latest result version.
func (uc *DocumentUseCase) SaveDraft(ctx context.Context, tenantID, docID, userID uuid.UUID, req domain.SaveDraftRequest) error {
	doc, err := uc.GetDocument(ctx, tenantID, docID)
	if err != nil {
		return err
	}
	if doc.Status != domain.StatusReviewRequired {
		return domain.ErrDocumentNotEditable
	}

	// Upsert result version draft
	now := time.Now().UTC()
	_, err = uc.db.Exec(ctx,
		`INSERT INTO result_versions (id, document_id, tenant_id, version, is_approved, approved_text, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3,
		   COALESCE((SELECT MAX(version) FROM result_versions WHERE document_id = $2), 0) + 1,
		   FALSE, $4, $5, $6, $7)
		 ON CONFLICT (document_id, version) DO UPDATE
		   SET approved_text = EXCLUDED.approved_text, updated_at = EXCLUDED.updated_at`,
		uuid.New(), docID, tenantID, req.Text, userID, now, now,
	)
	return err
}

// ApproveDocument marks a document's result as approved.
func (uc *DocumentUseCase) ApproveDocument(ctx context.Context, tenantID, docID, reviewerID uuid.UUID) error {
	doc, err := uc.GetDocument(ctx, tenantID, docID)
	if err != nil {
		return err
	}
	if doc.Status != domain.StatusReviewRequired {
		return domain.ErrInvalidTransition
	}

	now := time.Now().UTC()
	tx, err := uc.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx,
		`UPDATE documents SET status = 'APPROVED', approved_by = $1, approved_at = $2, updated_at = $3 WHERE id = $4`,
		reviewerID, now, now, docID,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE result_versions SET is_approved = TRUE, approved_by = $1, approved_at = $2
		 WHERE document_id = $3 AND version = (SELECT MAX(version) FROM result_versions WHERE document_id = $3)`,
		reviewerID, now, docID,
	)
	if err != nil {
		return err
	}

	// Audit event
	auditPayload, _ := json.Marshal(map[string]string{"document_id": docID.String(), "approved_by": reviewerID.String()})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_events (id, tenant_id, actor_id, action, resource_type, resource_id, occurred_at)
		 VALUES ($1, $2, $3, 'DOCUMENT_APPROVED', 'Document', $4, $5)`,
		uuid.New(), tenantID, reviewerID, docID, now,
	)
	if err != nil {
		uc.log.Warn("failed to write audit event", zap.Error(err), zap.Any("payload", auditPayload))
	}

	return tx.Commit(ctx)
}

// SoftDeleteDocument soft-deletes a document (sets deleted_at).
func (uc *DocumentUseCase) SoftDeleteDocument(ctx context.Context, tenantID, docID, userID uuid.UUID) error {
	result, err := uc.db.Exec(ctx,
		`UPDATE documents SET deleted_at = NOW(), status = 'DELETED', updated_at = NOW()
		 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		docID, tenantID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return domain.ErrDocumentNotFound
	}
	return nil
}
