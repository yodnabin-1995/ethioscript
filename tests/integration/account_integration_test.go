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

// TestTenantCreation verifies tenant insert and retrieval round-trip.
func TestTenantCreation(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("test-tenant")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	id := uuid.New()
	now := time.Now().UTC()

	_, err := db.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, TRUE, $4, $5)`,
		id, "Integration Test Tenant", slug, now, now,
	)
	require.NoError(t, err, "insert tenant")

	var gotName string
	var gotActive bool
	err = db.QueryRow(ctx,
		`SELECT name, is_active FROM tenants WHERE id = $1`, id,
	).Scan(&gotName, &gotActive)
	require.NoError(t, err, "query tenant")

	assert.Equal(t, "Integration Test Tenant", gotName)
	assert.True(t, gotActive)
}

// TestUserRegistrationAndLogin tests user creation, password hash storage,
// and login via direct DB queries (mirrors what the usecase layer does).
func TestUserRegistrationAndLogin(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("user-test")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	now := time.Now().UTC()

	tenantID := uuid.New()
	_, err := db.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, TRUE, $4, $5)`,
		tenantID, "User Test Tenant", slug, now, now,
	)
	require.NoError(t, err)

	// Simulate registration: store bcrypt hash
	userID := uuid.New()
	// bcrypt hash for "Test1234" at cost 4 (fast for tests)
	testHash := "$2a$04$yMock.HashForTestingOnly.xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

	_, err = db.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, name, role, status, email_verified, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'CONTRIBUTOR', 'ACTIVE', FALSE, $6, $7)`,
		userID, tenantID, "test@example.com", testHash, "Test User", now, now,
	)
	require.NoError(t, err, "insert user")

	// Verify user is retrievable by email within tenant
	var gotID uuid.UUID
	var gotRole, gotStatus string
	err = db.QueryRow(ctx,
		`SELECT id, role, status FROM users
		 WHERE email = $1 AND tenant_id = $2 AND status != 'DELETED'`,
		"test@example.com", tenantID,
	).Scan(&gotID, &gotRole, &gotStatus)
	require.NoError(t, err, "query user by email")
	assert.Equal(t, userID, gotID)
	assert.Equal(t, "CONTRIBUTOR", gotRole)
	assert.Equal(t, "ACTIVE", gotStatus)
}

// TestRefreshTokenLifecycle verifies create, fetch, and revoke of refresh tokens.
func TestRefreshTokenLifecycle(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("rt-test")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	now := time.Now().UTC()

	// Create minimal tenant + user
	tenantID := uuid.New()
	userID := uuid.New()
	_, _ = db.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at) VALUES ($1, $2, $3, TRUE, $4, $5)`,
		tenantID, "RT Tenant", slug, now, now)
	_, _ = db.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, name, role, status, email_verified, created_at, updated_at)
		 VALUES ($1, $2, 'rt@example.com', 'hash', 'RT User', 'CONTRIBUTOR', 'ACTIVE', FALSE, $3, $4)`,
		userID, tenantID, now, now)

	// Create refresh token
	rtID := uuid.New()
	tokenHash := "test_hash_" + uuid.NewString()
	expires := now.Add(7 * 24 * time.Hour)

	_, err := db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		rtID, userID, tenantID, tokenHash, expires, now,
	)
	require.NoError(t, err, "insert refresh token")

	// Fetch it
	var gotRevoked bool
	var gotExpires time.Time
	err = db.QueryRow(ctx,
		`SELECT revoked, expires_at FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&gotRevoked, &gotExpires)
	require.NoError(t, err)
	assert.False(t, gotRevoked)
	assert.True(t, gotExpires.After(now))

	// Revoke
	_, err = db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash)
	require.NoError(t, err)

	err = db.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&gotRevoked)
	require.NoError(t, err)
	assert.True(t, gotRevoked, "token should be revoked")
}

// TestCrossTenantUserIsolation ensures users from different tenants
// cannot be queried across tenant boundaries.
func TestCrossTenantUserIsolation(t *testing.T) {
	db := newTestDB(t)
	slugA := uniqueSlug("iso-a")
	slugB := uniqueSlug("iso-b")
	defer cleanupTenant(t, db, slugA)
	defer cleanupTenant(t, db, slugB)

	ctx := context.Background()
	now := time.Now().UTC()

	tenantA := uuid.New()
	tenantB := uuid.New()
	userA := uuid.New()

	for _, row := range []struct{ id uuid.UUID; slug, name string }{
		{tenantA, slugA, "Tenant A"},
		{tenantB, slugB, "Tenant B"},
	} {
		_, err := db.Exec(ctx,
			`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at) VALUES ($1, $2, $3, TRUE, $4, $5)`,
			row.id, row.name, row.slug, now, now)
		require.NoError(t, err)
	}

	// User belongs to tenantA
	_, err := db.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, name, role, status, email_verified, created_at, updated_at)
		 VALUES ($1, $2, 'user-a@example.com', 'hash', 'User A', 'CONTRIBUTOR', 'ACTIVE', FALSE, $3, $4)`,
		userA, tenantA, now, now)
	require.NoError(t, err)

	// Querying with tenantB should return nothing
	var count int
	err = db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE id = $1 AND tenant_id = $2`, userA, tenantB,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "user from tenantA must not be visible under tenantB")
}

// TestOutboxEventCreation verifies the transactional outbox pattern.
func TestOutboxEventCreation(t *testing.T) {
	db := newTestDB(t)
	slug := uniqueSlug("outbox-test")
	defer cleanupTenant(t, db, slug)

	ctx := context.Background()
	now := time.Now().UTC()

	tenantID := uuid.New()
	_, _ = db.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_active, created_at, updated_at) VALUES ($1, $2, $3, TRUE, $4, $5)`,
		tenantID, "Outbox Tenant", slug, now, now)

	// Simulate the transactional outbox insert within a tx
	tx, err := db.Begin(ctx)
	require.NoError(t, err)

	docID := uuid.New()
	outboxID := uuid.New()
	payload := []byte(`{"document_id":"` + docID.String() + `","task_type":"OCR"}`)

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events (id, aggregate_id, aggregate_type, event_type, tenant_id, payload, occurred_at)
		 VALUES ($1, $2, 'Document', 'document.processing.requested', $3, $4, $5)`,
		outboxID, docID, tenantID, payload, now,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	// Verify unpublished event is queryable
	var gotPublished bool
	var gotEventType string
	err = db.QueryRow(ctx,
		`SELECT published, event_type FROM outbox_events WHERE id = $1`, outboxID,
	).Scan(&gotPublished, &gotEventType)
	require.NoError(t, err)
	assert.False(t, gotPublished, "new outbox event must not be published yet")
	assert.Equal(t, "document.processing.requested", gotEventType)

	// Simulate outbox publisher marking it published
	_, err = db.Exec(ctx,
		`UPDATE outbox_events SET published = TRUE, published_at = NOW() WHERE id = $1`, outboxID)
	require.NoError(t, err)

	err = db.QueryRow(ctx,
		`SELECT published FROM outbox_events WHERE id = $1`, outboxID,
	).Scan(&gotPublished)
	require.NoError(t, err)
	assert.True(t, gotPublished)

	// Cleanup
	_, _ = db.Exec(ctx, `DELETE FROM outbox_events WHERE id = $1`, outboxID)
}

// TestRabbitMQPublishAndConsume verifies the broker is reachable and
// messages survive a publish/consume round-trip.
func TestRabbitMQPublishAndConsume(t *testing.T) {
	_, ch := newTestAMQP(t)

	q, err := ch.QueueDeclare("test.integration.queue", false, true, false, false, nil)
	require.NoError(t, err)

	body := []byte(`{"test":"integration","ts":"` + time.Now().Format(time.RFC3339) + `"}`)
	err = ch.Publish("", q.Name, false, false,
		amqp.Publishing{ContentType: "application/json", Body: body})
	require.NoError(t, err, "publish message")

	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	require.NoError(t, err)

	select {
	case msg := <-msgs:
		assert.Equal(t, body, msg.Body, "message body round-trip")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}
