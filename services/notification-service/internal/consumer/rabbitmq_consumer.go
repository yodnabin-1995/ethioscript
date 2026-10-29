// Package consumer subscribes to the RabbitMQ exchange and creates notifications
// for job and document events published by other EthioScript services.
package consumer

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/repository"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

const (
	exchange = "ethioscript.events"
	queue    = "ethioscript.notifications.events"
)

// bindingKeys are the routing keys this service subscribes to.
var bindingKeys = []string{
	"jobs.results",
	"document.processing.requested",
	"notifications.inbox",
	"notifications.push",
}

// Consumer listens to RabbitMQ and creates inbox notifications.
type Consumer struct {
	amqpURL string
	repo    *repository.NotificationRepo
	log     *zap.Logger
}

func New(amqpURL string, repo *repository.NotificationRepo, log *zap.Logger) *Consumer {
	return &Consumer{amqpURL: amqpURL, repo: repo, log: log}
}

// Run connects and consumes messages; reconnects with backoff on failure.
// Blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			c.log.Info("rabbitmq consumer stopped")
			return
		default:
		}

		if err := c.connect(ctx); err != nil {
			c.log.Error("rabbitmq consumer error — retrying",
				zap.Error(err), zap.Duration("backoff", backoff))
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < 60*time.Second {
					backoff *= 2
				}
			}
		} else {
			backoff = 5 * time.Second
		}
	}
}

func (c *Consumer) connect(ctx context.Context) error {
	conn, err := amqp.Dial(c.amqpURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Declare exchange (idempotent — matches what other services declare)
	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}

	// Declare our durable queue
	q, err := ch.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		return err
	}

	// Bind all routing keys
	for _, key := range bindingKeys {
		if err := ch.QueueBind(q.Name, key, exchange, false, nil); err != nil {
			return err
		}
		c.log.Info("rabbitmq bound", zap.String("queue", q.Name), zap.String("key", key))
	}

	if err := ch.Qos(10, 0, false); err != nil {
		return err
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	c.log.Info("rabbitmq consumer ready",
		zap.String("exchange", exchange), zap.String("queue", queue))

	connClose := conn.NotifyClose(make(chan *amqp.Error, 1))

	for {
		select {
		case <-ctx.Done():
			return nil
		case amqpErr := <-connClose:
			if amqpErr != nil {
				return amqpErr
			}
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			c.handle(ctx, msg)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, msg amqp.Delivery) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("panic in consumer handler", zap.Any("panic", r))
			_ = msg.Nack(false, false)
		}
	}()

	key := msg.RoutingKey

	var raw map[string]any
	if err := json.Unmarshal(msg.Body, &raw); err != nil {
		c.log.Warn("rabbitmq: malformed JSON", zap.String("routing_key", key))
		_ = msg.Ack(false) // don't requeue bad JSON
		return
	}

	// Events from document-service / job-orchestrator use an envelope with a "payload" field.
	// Unwrap it and promote useful envelope fields into the payload map.
	body := raw
	if payload, ok := raw["payload"].(map[string]any); ok {
		for _, k := range []string{"tenant_id", "aggregate_id", "event_type"} {
			if v, exists := raw[k]; exists {
				payload[k] = v
			}
		}
		body = payload
	}

	c.log.Info("rabbitmq event received", zap.String("routing_key", key))

	var handlerErr error
	switch key {
	case "jobs.results":
		handlerErr = c.handleJobResult(ctx, body)
	case "document.processing.requested":
		handlerErr = c.handleDocumentRequested(ctx, body)
	case "notifications.inbox", "notifications.push":
		handlerErr = c.handleDirectNotification(ctx, key, body)
	default:
		c.log.Info("rabbitmq: unhandled routing key", zap.String("key", key))
	}

	if handlerErr != nil {
		c.log.Error("rabbitmq handler error",
			zap.String("key", key), zap.Error(handlerErr))
		_ = msg.Nack(false, true) // requeue for retry
		return
	}

	_ = msg.Ack(false)
}

// handleJobResult processes jobs.results events from ocr-worker / stt-worker.
//
// Payload fields (from events.md):
//
//	job_id, document_id, tenant_id, success, raw_text_key, model_name, ...
//
// user_id is NOT in the worker payload — we look it up via document_id → documents.created_by.
func (c *Consumer) handleJobResult(ctx context.Context, body map[string]any) error {
	docIDStr, _ := body["document_id"].(string)
	success, _ := body["success"].(bool)
	jobID, _ := body["job_id"].(string)

	if docIDStr == "" {
		c.log.Warn("jobs.results: missing document_id in payload")
		return nil
	}

	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		c.log.Warn("jobs.results: invalid document_id", zap.String("document_id", docIDStr))
		return nil
	}

	userID, err := c.repo.LookupUserIDByDocumentID(ctx, docID)
	if err == domain.ErrNotFound {
		c.log.Warn("jobs.results: document not found — no notification sent",
			zap.String("document_id", docIDStr))
		return nil // not an error we should retry
	}
	if err != nil {
		return err
	}

	actionURL := strPtr("/documents/" + docIDStr)
	meta := map[string]any{"job_id": jobID, "document_id": docIDStr}

	if success {
		_, err = c.repo.Create(ctx, userID,
			domain.TypeJobCompleted, domain.ChannelInApp,
			"✅ Processing completed",
			"Your document has been processed successfully.",
			actionURL, meta)
	} else {
		_, err = c.repo.Create(ctx, userID,
			domain.TypeJobFailed, domain.ChannelInApp,
			"❌ Processing failed",
			"Your document could not be processed. Please check and retry.",
			actionURL, meta)
	}
	return err
}

// handleDocumentRequested processes document.processing.requested events.
//
// Payload fields (from events.md): document_id, job_id, tenant_id, task_type, language.
// user_id resolved via document_id → documents.created_by.
func (c *Consumer) handleDocumentRequested(ctx context.Context, body map[string]any) error {
	docIDStr, _ := body["document_id"].(string)
	if docIDStr == "" {
		return nil
	}

	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		return nil
	}

	userID, err := c.repo.LookupUserIDByDocumentID(ctx, docID)
	if err == domain.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	_, err = c.repo.Create(ctx, userID,
		domain.TypeDocumentReady, domain.ChannelInApp,
		"📄 Document queued for processing",
		"Your document has been received and will be processed shortly.",
		strPtr("/documents/"+docIDStr),
		map[string]any{"document_id": docIDStr},
	)
	return err
}

// handleDirectNotification processes notifications.inbox and notifications.push events
// published directly by other services (not wrapped in the standard envelope).
func (c *Consumer) handleDirectNotification(ctx context.Context, routingKey string, body map[string]any) error {
	userIDStr, _ := body["user_id"].(string)
	if userIDStr == "" {
		c.log.Warn("direct notification event: missing user_id")
		return nil
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.log.Warn("direct notification event: invalid user_id", zap.String("user_id", userIDStr))
		return nil
	}

	nType := domain.TypeSystemAlert
	if t, ok := body["type"].(string); ok && t != "" {
		nType = domain.NotificationType(t)
	}

	channel := domain.ChannelInApp
	if routingKey == "notifications.push" {
		channel = domain.ChannelPush
	}

	title, _ := body["title"].(string)
	bodyText, _ := body["body"].(string)
	if title == "" {
		title = "Notification"
	}

	var actionURL *string
	if u, ok := body["action_url"].(string); ok && u != "" {
		actionURL = &u
	}

	var meta map[string]any
	if m, ok := body["metadata"].(map[string]any); ok {
		meta = m
	}

	_, err = c.repo.Create(ctx, userID, nType, channel, title, bodyText, actionURL, meta)
	return err
}

func strPtr(s string) *string { return &s }
