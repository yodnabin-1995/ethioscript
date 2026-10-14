// Package integration contains integration tests that run against real
// infrastructure (PostgreSQL, Redis, RabbitMQ). Start the stack first:
//
//	make up-infra
//
// Then run:
//
//	go test ./... -v -tags=integration
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

// ─── Environment helpers ──────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ─── Shared fixtures ──────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := getEnv("POSTGRES_DSN",
		"postgres://ethio:ethiopass@localhost:6300/ethioscript?sslmode=disable")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := getEnv("REDIS_ADDR", "localhost:6301")
	pass := getEnv("REDIS_PASSWORD", "redispass")

	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	t.Cleanup(func() {
		rdb.FlushDB(context.Background()) // clean test DB slot
		rdb.Close()
	})
	return rdb
}

func newTestAMQP(t *testing.T) (*amqp.Connection, *amqp.Channel) {
	t.Helper()
	url := getEnv("RABBITMQ_URL",
		"amqp://ethio:ethiopass@localhost:6304/ethioscript")

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("connect rabbitmq: %v", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	t.Cleanup(func() { ch.Close(); conn.Close() })
	return conn, ch
}

// cleanupTenant deletes all test data for a given tenant slug.
func cleanupTenant(t *testing.T, db *pgxpool.Pool, slug string) {
	t.Helper()
	ctx := context.Background()
	_, err := db.Exec(ctx,
		`DELETE FROM tenants WHERE slug = $1`, slug)
	if err != nil {
		t.Logf("cleanup warning: %v", err)
	}
}

// uniqueSlug returns a tenant slug unique per test run.
func uniqueSlug(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
