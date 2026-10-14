package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ─── Status & enums ───────────────────────────────────────────────────────────

type DocumentStatus string

const (
	StatusUploaded        DocumentStatus = "UPLOADED"
	StatusQueued          DocumentStatus = "QUEUED"
	StatusProcessing      DocumentStatus = "PROCESSING"
	StatusReviewRequired  DocumentStatus = "REVIEW_REQUIRED"
	StatusApproved        DocumentStatus = "APPROVED"
	StatusFailed          DocumentStatus = "FAILED"
	StatusCancelled       DocumentStatus = "CANCELLED"
	StatusArchived        DocumentStatus = "ARCHIVED"
	StatusDeleted         DocumentStatus = "DELETED"
)

type TaskType string

const (
	TaskOCR            TaskType = "OCR"
	TaskSTT            TaskType = "STT"
	TaskClassification TaskType = "CLASSIFICATION"
	TaskExport         TaskType = "EXPORT"
)

// ─── Domain errors ────────────────────────────────────────────────────────────

var (
	ErrDocumentNotFound    = errors.New("document not found")
	ErrForbidden           = errors.New("access denied")
	ErrInvalidTransition   = errors.New("invalid status transition")
	ErrDocumentNotEditable = errors.New("document is not in an editable state")
)

// ─── Aggregate ───────────────────────────────────────────────────────────────

// Document is the core aggregate for a submitted file.
type Document struct {
	ID              uuid.UUID      `json:"id"`
	TenantID        uuid.UUID      `json:"tenant_id"`
	AssetID         uuid.UUID      `json:"asset_id"`
	Filename        string         `json:"filename"`
	Language        string         `json:"language"`
	TaskType        TaskType       `json:"task_type"`
	Status          DocumentStatus `json:"status"`
	Title           *string        `json:"title,omitempty"`
	Description     *string        `json:"description,omitempty"`
	Tags            []string       `json:"tags"`
	PageCount       *int           `json:"page_count,omitempty"`
	DurationSeconds *float64       `json:"duration_seconds,omitempty"`
	CreatedBy       uuid.UUID      `json:"created_by"`
	AssignedTo      *uuid.UUID     `json:"assigned_to,omitempty"`
	ApprovedBy      *uuid.UUID     `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time     `json:"approved_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// ─── Lifecycle / state machine ────────────────────────────────────────────────

// validTransitions defines allowed status moves.
var validTransitions = map[DocumentStatus][]DocumentStatus{
	StatusUploaded:       {StatusQueued, StatusCancelled},
	StatusQueued:         {StatusProcessing, StatusCancelled},
	StatusProcessing:     {StatusReviewRequired, StatusCompleted, StatusFailed, StatusCancelled},
	StatusReviewRequired: {StatusApproved, StatusFailed, StatusCancelled},
	StatusApproved:       {StatusArchived},
	StatusFailed:         {StatusQueued},    // retry
	StatusCancelled:      {},
	StatusArchived:       {},
	StatusDeleted:        {},
}

// we use COMPLETED as an alias for APPROVED at domain level when no review needed
const StatusCompleted DocumentStatus = StatusApproved

// CanTransitionTo returns true if the status move is allowed.
func (d *Document) CanTransitionTo(next DocumentStatus) bool {
	allowed, ok := validTransitions[d.Status]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == next {
			return true
		}
	}
	return false
}

// TransitionTo applies a status transition, enforcing the state machine.
func (d *Document) TransitionTo(next DocumentStatus) error {
	if !d.CanTransitionTo(next) {
		return ErrInvalidTransition
	}
	d.Status = next
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// ─── Value objects ────────────────────────────────────────────────────────────

type ResultVersion struct {
	ID               uuid.UUID  `json:"id"`
	DocumentID       uuid.UUID  `json:"document_id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	JobID            *uuid.UUID `json:"job_id,omitempty"`
	Version          int        `json:"version"`
	IsApproved       bool       `json:"is_approved"`
	RawTextKey       *string    `json:"raw_text_key,omitempty"`
	ApprovedText     *string    `json:"approved_text,omitempty"`
	ConfidenceScore  *float64   `json:"confidence_score,omitempty"`
	ModelMetadata    any        `json:"model_metadata"`
	WordCount        *int       `json:"word_count,omitempty"`
	CharCount        *int       `json:"char_count,omitempty"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	ApprovedBy       *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ─── Request DTOs ─────────────────────────────────────────────────────────────

type CreateDocumentRequest struct {
	AssetID     string   `json:"asset_id"`
	Filename    string   `json:"filename"`
	Language    string   `json:"language"`
	TaskType    TaskType `json:"task_type"`
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type SaveDraftRequest struct {
	Text string `json:"text"`
}

type ApproveRequest struct {
	VersionNumber int    `json:"version"`
	Comment       string `json:"comment,omitempty"`
}

type ListDocumentsParams struct {
	TenantID uuid.UUID
	Status   *DocumentStatus
	Language *string
	TaskType *TaskType
	Page     int
	PageSize int
}

type DocumentListResponse struct {
	Documents  []Document `json:"documents"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	TotalPages int        `json:"total_pages"`
}
