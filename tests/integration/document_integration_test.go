//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDocumentJobAtomicCreation verifies that document + job + outbox event
// are created together in one transaction (all-or-nothing).
func TestDocumentJobAtomicCreation(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("doc-job")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	now := time.Now().UTC()

	tenantID := uuid.New()
	userID := uuid.New()
	assetID := uuid.New()

	// Seed tenant + user + asset
	_, _ = db.Exec(ctx,
		`INSERT INTO tenants (id,name,slug,is_active,created_at,updated_at) VALUES ($1,$2,$3,TRUE,$4,$5)`,
		tenantID, "Doc Tenant", slug, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO users (id,tenant_id,email,password_hash,name,role,status,email_verified,created_at,updated_at)
		 VALUES ($1,$2,'doc@example.com','hash','Doc User','CONTRIBUTOR','ACTIVE',FALSE,$3,$4)`,
		userID, tenantID, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO assets (id,tenant_id,object_key,original_filename,checksum_sha256,media_type,size_bytes,bucket,scan_status,created_by,created_at)
		 VALUES ($1,$2,'key/doc.pdf','doc.pdf','abc123','application/pdf',1024,'ethioscript-assets','PENDING',$3,$4)`,
		assetID, tenantID, userID, now)

	// Atomic: document + job + outbox in one tx
	docID := uuid.New()
	jobID := uuid.New()
	outboxID := uuid.New()
	payload := []byte(`{"document_id":"` + docID.String() + `","job_id":"` + jobID.String() + `","task_type":"OCR"}`)

	tx, err := db.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx,
		`INSERT INTO documents (id,tenant_id,asset_id,filename,language,task_type,status,tags,created_by,created_at,updated_at)
		 VALUES ($1,$2,$3,'doc.pdf','amh','OCR','QUEUED','{}', $4,$5,$6)`,
		docID, tenantID, assetID, userID, now, now)
	require.NoError(t, err)

	_, err = tx.Exec(ctx,
		`INSERT INTO processing_jobs (id,document_id,tenant_id,task_type,state,language,created_at,updated_at)
		 VALUES ($1,$2,$3,'OCR','PENDING','amh',$4,$5)`,
		jobID, docID, tenantID, now, now)
	require.NoError(t, err)

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events (id,aggregate_id,aggregate_type,event_type,tenant_id,payload,occurred_at)
		 VALUES ($1,$2,'Document','document.processing.requested',$3,$4,$5)`,
		outboxID, docID, tenantID, payload, now)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	// Verify all three rows exist
	var docStatus, jobState string
	err = db.QueryRow(ctx, `SELECT status FROM documents WHERE id = $1`, docID).Scan(&docStatus)
	require.NoError(t, err)
	assert.Equal(t, "QUEUED", docStatus)

	err = db.QueryRow(ctx, `SELECT state FROM processing_jobs WHERE id = $1`, jobID).Scan(&jobState)
	require.NoError(t, err)
	assert.Equal(t, "PENDING", jobState)

	var outboxCount int
	err = db.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE id = $1`, outboxID).Scan(&outboxCount)
	require.NoError(t, err)
	assert.Equal(t, 1, outboxCount)

	// Cleanup
	_, _ = db.Exec(ctx, `DELETE FROM outbox_events WHERE id = $1`, outboxID)
	_, _ = db.Exec(ctx, `DELETE FROM processing_jobs WHERE id = $1`, jobID)
	_, _ = db.Exec(ctx, `DELETE FROM documents WHERE id = $1`, docID)
	_, _ = db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, assetID)
}

// TestJobStateTransitions tests the valid job state machine transitions in the DB.
func TestJobStateTransitions(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("job-state")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	now := time.Now().UTC()

	tenantID := uuid.New()
	userID := uuid.New()
	assetID := uuid.New()
	docID := uuid.New()
	jobID := uuid.New()

	// Seed
	_, _ = db.Exec(ctx,
		`INSERT INTO tenants (id,name,slug,is_active,created_at,updated_at) VALUES ($1,'ST Tenant',$2,TRUE,$3,$4)`,
		tenantID, slug, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO users (id,tenant_id,email,password_hash,name,role,status,email_verified,created_at,updated_at)
		 VALUES ($1,$2,'st@example.com','hash','ST User','CONTRIBUTOR','ACTIVE',FALSE,$3,$4)`,
		userID, tenantID, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO assets (id,tenant_id,object_key,original_filename,checksum_sha256,media_type,size_bytes,bucket,scan_status,created_by,created_at)
		 VALUES ($1,$2,'k','f.pdf','h','application/pdf',1,'b','PENDING',$3,$4)`,
		assetID, tenantID, userID, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO documents (id,tenant_id,asset_id,filename,language,task_type,status,tags,created_by,created_at,updated_at)
		 VALUES ($1,$2,$3,'f.pdf','amh','OCR','QUEUED','{}', $4,$5,$6)`,
		docID, tenantID, assetID, userID, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO processing_jobs (id,document_id,tenant_id,task_type,state,language,created_at,updated_at)
		 VALUES ($1,$2,$3,'OCR','PENDING','amh',$4,$5)`,
		jobID, docID, tenantID, now, now)

	transitions := []struct {
		to      string
		expectQ string
	}{
		{"QUEUED", "QUEUED"},
		{"PROCESSING", "PROCESSING"},
		{"REVIEW_REQUIRED", "REVIEW_REQUIRED"},
		{"COMPLETED", "COMPLETED"},
	}

	for _, tr := range transitions {
		_, err := db.Exec(ctx,
			`UPDATE processing_jobs SET state = $1, updated_at = NOW() WHERE id = $2`, tr.to, jobID)
		require.NoErrorf(t, err, "transition to %s", tr.to)

		var state string
		_ = db.QueryRow(ctx, `SELECT state FROM processing_jobs WHERE id = $1`, jobID).Scan(&state)
		assert.Equal(t, tr.expectQ, state)
	}

	// Cleanup
	_, _ = db.Exec(ctx, `DELETE FROM processing_jobs WHERE id = $1`, jobID)
	_, _ = db.Exec(ctx, `DELETE FROM documents WHERE id = $1`, docID)
	_, _ = db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, assetID)
}
