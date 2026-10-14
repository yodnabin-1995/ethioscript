package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ─── Enums ────────────────────────────────────────────────────────────────────

type JobState string

const (
	StatePending         JobState = "PENDING"
	StateQueued          JobState = "QUEUED"
	StateProcessing      JobState = "PROCESSING"
	StateReviewRequired  JobState = "REVIEW_REQUIRED"
	StateCompleted       JobState = "COMPLETED"
	StateFailed          JobState = "FAILED"
	StateCancelled       JobState = "CANCELLED"
	StateRetrying        JobState = "RETRYING"
)

type TaskType string

const (
	TaskOCR TaskType = "OCR"
	TaskSTT TaskType = "STT"
)

// ─── Errors ───────────────────────────────────────────────────────────────────

var (
	ErrJobNotFound       = errors.New("job not found")
	ErrInvalidTransition = errors.New("invalid job state transition")
	ErrJobNotRetryable   = errors.New("job has exhausted retry attempts")
	ErrJobNotCancellable = errors.New("job cannot be cancelled in its current state")
)

// ─── Aggregate ───────────────────────────────────────────────────────────────

type Job struct {
	ID               uuid.UUID  `json:"id"`
	DocumentID       uuid.UUID  `json:"document_id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	TaskType         TaskType   `json:"task_type"`
	State            JobState   `json:"state"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	WorkerID         *string    `json:"worker_id,omitempty"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	ModelName        *string    `json:"model_name,omitempty"`
	ModelVersion     *string    `json:"model_version,omitempty"`
	Language         *string    `json:"language,omitempty"`
	ErrorCode        *string    `json:"error_code,omitempty"`
	ErrorMessage     *string    `json:"error_message,omitempty"`
	RetryAfter       *time.Time `json:"retry_after,omitempty"`
	Priority         int        `json:"priority"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ─── State machine ────────────────────────────────────────────────────────────

var validTransitions = map[JobState][]JobState{
	StatePending:        {StateQueued, StateCancelled},
	StateQueued:         {StateProcessing, StateCancelled},
	StateProcessing:     {StateReviewRequired, StateCompleted, StateFailed, StateCancelled},
	StateReviewRequired: {StateCompleted, StateFailed},
	StateFailed:         {StateRetrying, StateCancelled},
	StateRetrying:       {StateQueued, StateCancelled},
	StateCompleted:      {},
	StateCancelled:      {},
}

func (j *Job) CanTransitionTo(next JobState) bool {
	for _, allowed := range validTransitions[j.State] {
		if allowed == next {
			return true
		}
	}
	return false
}

func (j *Job) TransitionTo(next JobState) error {
	if !j.CanTransitionTo(next) {
		return ErrInvalidTransition
	}
	j.State = next
	j.UpdatedAt = time.Now().UTC()
	return nil
}

// IsRetryable returns true if the job has attempts remaining.
func (j *Job) IsRetryable() bool {
	return j.Attempts < j.MaxAttempts
}

// ─── Worker result payload ────────────────────────────────────────────────────

type JobResult struct {
	JobID         string         `json:"job_id"`
	DocumentID    string         `json:"document_id"`
	TenantID      string         `json:"tenant_id"`
	Success       bool           `json:"success"`
	ErrorCode     string         `json:"error_code,omitempty"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	RawTextKey    string         `json:"raw_text_key,omitempty"`
	ModelName     string         `json:"model_name"`
	ModelVersion  string         `json:"model_version"`
	Language      string         `json:"language"`
	Confidence    float64        `json:"confidence_score,omitempty"`
	PageCount     int            `json:"page_count,omitempty"`
	WordCount     int            `json:"word_count,omitempty"`
	CharCount     int            `json:"char_count,omitempty"`
	ProcessedAt   time.Time      `json:"processed_at"`
}
