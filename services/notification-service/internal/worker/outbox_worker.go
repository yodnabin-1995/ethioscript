// Package worker runs the transactional outbox dispatcher.
// It polls notification_outbox and dispatches pending rows to their channels.
package worker

import (
	"context"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/repository"
	"go.uber.org/zap"
)

// OutboxWorker polls the outbox table and dispatches pending notifications.
type OutboxWorker struct {
	repo     *repository.NotificationRepo
	poll     time.Duration
	log      *zap.Logger
}

func NewOutboxWorker(repo *repository.NotificationRepo, poll time.Duration, log *zap.Logger) *OutboxWorker {
	return &OutboxWorker{repo: repo, poll: poll, log: log}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (w *OutboxWorker) Run(ctx context.Context) {
	w.log.Info("outbox worker started", zap.Duration("poll_interval", w.poll))
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("outbox worker stopped")
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *OutboxWorker) process(ctx context.Context) {
	entries, err := w.repo.PendingOutbox(ctx, 50)
	if err != nil {
		w.log.Error("outbox: query failed", zap.Error(err))
		return
	}

	for _, e := range entries {
		w.dispatch(ctx, e)
	}
}

func (w *OutboxWorker) dispatch(ctx context.Context, e domain.OutboxEntry) {
	var dispatchErr error

	switch e.Channel {
	case domain.ChannelInApp:
		// IN_APP: mark the notification as DELIVERED — delivery is reading from the inbox.
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)

	case domain.ChannelEmail:
		// Stub: log the email. In production, call SMTP / SES / SendGrid here.
		w.log.Info("outbox: EMAIL stub",
			zap.String("notification_id", e.NotificationID.String()),
			zap.Any("payload", e.Payload),
			zap.String("note", "plug in SMTP/SES/SendGrid provider"),
		)
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)

	case domain.ChannelSMS:
		// Stub: log the SMS. In production, call Twilio / Africa's Talking here.
		w.log.Info("outbox: SMS stub",
			zap.String("notification_id", e.NotificationID.String()),
			zap.Any("payload", e.Payload),
			zap.String("note", "plug in Twilio / Africa's Talking provider"),
		)
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)

	case domain.ChannelTelegram:
		// Stub: log the Telegram message. In production, call Telegram Bot API here.
		w.log.Info("outbox: TELEGRAM stub",
			zap.String("notification_id", e.NotificationID.String()),
			zap.Any("payload", e.Payload),
			zap.String("note", "plug in Telegram Bot API"),
		)
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)

	case domain.ChannelPush:
		// Push delivery is handled separately — mark outbox row as processed.
		// A dedicated push worker would fan out to push_subscriptions here.
		w.log.Info("outbox: PUSH — VAPID delivery not yet implemented",
			zap.String("notification_id", e.NotificationID.String()),
			zap.String("note", "implement VAPID push delivery here"),
		)
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)

	default:
		w.log.Warn("outbox: unknown channel", zap.String("channel", string(e.Channel)))
		dispatchErr = w.repo.MarkDelivered(ctx, e.NotificationID)
	}

	if dispatchErr != nil {
		w.log.Error("outbox: dispatch failed",
			zap.String("outbox_id", e.ID.String()),
			zap.String("channel", string(e.Channel)),
			zap.Error(dispatchErr),
		)
		backoff := backoffSeconds(e.Attempts)
		_ = w.repo.RetryOutbox(ctx, e.ID, backoff, dispatchErr.Error())
		return
	}

	if err := w.repo.MarkOutboxProcessed(ctx, e.ID); err != nil {
		w.log.Error("outbox: mark processed failed", zap.Error(err))
	}
}

// backoffSeconds returns exponential backoff capped at 1 hour.
func backoffSeconds(attempts int) int {
	backoff := 60 * (1 << attempts) // 60, 120, 240, 480, ...
	if backoff > 3600 {
		return 3600
	}
	return backoff
}
