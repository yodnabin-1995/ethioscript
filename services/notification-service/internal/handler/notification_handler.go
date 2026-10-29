package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/ethioscript/notification-service/internal/middleware"
	"github.com/ethioscript/notification-service/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NotificationHandler handles all inbox endpoints.
type NotificationHandler struct {
	repo *repository.NotificationRepo
	log  *zap.Logger
}

func NewNotificationHandler(repo *repository.NotificationRepo, log *zap.Logger) *NotificationHandler {
	return &NotificationHandler{repo: repo, log: log}
}

// GetInbox godoc
//
//	@Summary		Get notification inbox
//	@Description	Returns paginated notifications for the authenticated user, newest first
//	@Tags			inbox
//	@Produce		json
//	@Security		BearerAuth
//	@Param			page        query   int     false   "Page number (default 1)"
//	@Param			page_size   query   int     false   "Items per page (default 20, max 100)"
//	@Param			unread_only query   bool    false   "Return only unread notifications"
//	@Success		200	{object}	domain.InboxResponse
//	@Failure		401	{object}	domain.ErrorResponse
//	@Router			/notifications [get]
func (h *NotificationHandler) GetInbox(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	unreadOnly := r.URL.Query().Get("unread_only") == "true"

	list, total, unread, err := h.repo.ListByUser(r.Context(), userID, page, pageSize, unreadOnly)
	if err != nil {
		h.log.Error("list notifications", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not fetch notifications", "INTERNAL")
		return
	}

	if list == nil {
		list = []domain.Notification{}
	}

	middleware.WriteJSON(w, http.StatusOK, domain.InboxResponse{
		Notifications: list,
		Total:         total,
		Page:          page,
		PageSize:      pageSize,
		UnreadCount:   unread,
	})
}

// GetUnreadCount godoc
//
//	@Summary		Get unread notification count
//	@Description	Returns the count of unread notifications for the authenticated user
//	@Tags			inbox
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.UnreadCountResponse
//	@Failure		401	{object}	domain.ErrorResponse
//	@Router			/notifications/unread-count [get]
func (h *NotificationHandler) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	count, err := h.repo.UnreadCount(r.Context(), userID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not fetch count", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, domain.UnreadCountResponse{UnreadCount: count})
}

// MarkRead godoc
//
//	@Summary		Mark notification as read
//	@Description	Marks a single notification as read for the authenticated user
//	@Tags			inbox
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Notification UUID"
//	@Success		200	{object}	domain.MarkReadResponse
//	@Failure		400	{object}	domain.ErrorResponse
//	@Failure		404	{object}	domain.ErrorResponse
//	@Router			/notifications/{id}/read [patch]
func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	idStr := chi.URLParam(r, "id")

	notifID, err := uuid.Parse(idStr)
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid notification id", "BAD_REQUEST")
		return
	}

	readAt, err := h.repo.MarkRead(r.Context(), notifID, userID)
	if err == domain.ErrNotFound {
		middleware.WriteError(w, r, http.StatusNotFound, "notification not found", "NOT_FOUND")
		return
	}
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not mark read", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, domain.MarkReadResponse{ID: notifID, ReadAt: readAt})
}

// MarkAllRead godoc
//
//	@Summary		Mark all notifications as read
//	@Description	Marks every unread notification as read for the authenticated user
//	@Tags			inbox
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.MarkAllReadResponse
//	@Failure		401	{object}	domain.ErrorResponse
//	@Router			/notifications/mark-all-read [post]
func (h *NotificationHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	count, err := h.repo.MarkAllRead(r.Context(), userID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not mark all read", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, domain.MarkAllReadResponse{MarkedCount: count})
}

// ─── Internal event ───────────────────────────────────────────────────────────

// CreateInternalEvent godoc
//
//	@Summary		Create notification from internal event
//	@Description	Used by other EthioScript microservices to push a notification via HTTP (alternative to RabbitMQ). Requires X-Internal-Key header.
//	@Tags			internal
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.InternalEventRequest	true	"Event payload"
//	@Success		201		{object}	domain.InternalEventResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/internal/notification-events [post]
func (h *NotificationHandler) CreateInternalEvent(w http.ResponseWriter, r *http.Request) {
	var req domain.InternalEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if req.UserID == "" || req.Title == "" || req.Body == "" {
		middleware.WriteError(w, r, http.StatusBadRequest, "user_id, title, and body are required", "BAD_REQUEST")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user_id", "BAD_REQUEST")
		return
	}

	channel := req.Channel
	if channel == "" {
		channel = domain.ChannelInApp
	}

	n, err := h.repo.Create(r.Context(), userID, req.EventType, channel, req.Title, req.Body, req.ActionURL, req.Metadata)
	if err != nil {
		h.log.Error("create internal event", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not create notification", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, domain.InternalEventResponse{
		NotificationID: n.ID,
		Message:        "notification created successfully",
	})
}

// ─── Bulk ─────────────────────────────────────────────────────────────────────

// SendBulk godoc
//
//	@Summary		Send bulk notification
//	@Description	Broadcasts a notification to multiple users (max 1000). Requires X-Internal-Key header.
//	@Tags			bulk
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.BulkNotificationRequest	true	"Bulk payload"
//	@Success		202		{object}	domain.BulkNotificationResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Failure		401		{object}	domain.ErrorResponse
//	@Router			/notifications/bulk [post]
func (h *NotificationHandler) SendBulk(w http.ResponseWriter, r *http.Request) {
	var req domain.BulkNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if len(req.UserIDs) == 0 {
		middleware.WriteError(w, r, http.StatusBadRequest, "user_ids is required", "BAD_REQUEST")
		return
	}
	if len(req.UserIDs) > 1000 {
		middleware.WriteError(w, r, http.StatusBadRequest, "max 1000 users per bulk request", "BAD_REQUEST")
		return
	}

	channel := req.Channel
	if channel == "" {
		channel = domain.ChannelInApp
	}

	// Fire-and-forget: insert in background so we respond fast
	go func() {
		for _, idStr := range req.UserIDs {
			uid, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			if _, err := h.repo.Create(
				r.Context(), uid, req.Type, channel,
				req.Title, req.Body, req.ActionURL, req.Metadata,
			); err != nil {
				h.log.Warn("bulk insert failed for user", zap.String("user_id", idStr), zap.Error(err))
			}
		}
	}()

	middleware.WriteJSON(w, http.StatusAccepted, domain.BulkNotificationResponse{
		QueuedCount: len(req.UserIDs),
		Message:     "bulk notification queued for " + strconv.Itoa(len(req.UserIDs)) + " users",
	})
}

// ─── Channel stubs (email / sms / telegram) ───────────────────────────────────

// SendEmail godoc
//
//	@Summary		Send email notification
//	@Description	Queues an email notification via the outbox worker. Requires X-Internal-Key header.
//	@Tags			channels
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.EmailNotificationRequest	true	"Email payload"
//	@Success		202		{object}	domain.InternalEventResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/notifications/email [post]
func (h *NotificationHandler) SendEmail(w http.ResponseWriter, r *http.Request) {
	var req domain.EmailNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user_id", "BAD_REQUEST")
		return
	}

	meta := req.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["to_email"] = req.ToEmail
	meta["body_html"] = req.BodyHTML
	if req.TemplateID != "" {
		meta["template_id"] = req.TemplateID
	}

	n, err := h.repo.Create(r.Context(), userID, domain.TypeSystemAlert, domain.ChannelEmail,
		req.Subject, req.BodyText, nil, meta)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not queue email", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusAccepted, domain.InternalEventResponse{
		NotificationID: n.ID,
		Message:        "email queued for delivery",
	})
}

// SendSMS godoc
//
//	@Summary		Send SMS notification
//	@Description	Queues an SMS notification via the outbox worker. Requires X-Internal-Key header.
//	@Tags			channels
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.SMSNotificationRequest	true	"SMS payload"
//	@Success		202		{object}	domain.InternalEventResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/notifications/sms [post]
func (h *NotificationHandler) SendSMS(w http.ResponseWriter, r *http.Request) {
	var req domain.SMSNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user_id", "BAD_REQUEST")
		return
	}

	meta := req.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["phone_number"] = req.PhoneNumber

	n, err := h.repo.Create(r.Context(), userID, domain.TypeSystemAlert, domain.ChannelSMS,
		"SMS Notification", req.Message, nil, meta)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not queue sms", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusAccepted, domain.InternalEventResponse{
		NotificationID: n.ID,
		Message:        "sms queued for delivery",
	})
}

// SendTelegram godoc
//
//	@Summary		Send Telegram notification
//	@Description	Queues a Telegram message via the outbox worker. Requires X-Internal-Key header.
//	@Tags			channels
//	@Accept			json
//	@Produce		json
//	@Param			body	body		domain.TelegramNotificationRequest	true	"Telegram payload"
//	@Success		202		{object}	domain.InternalEventResponse
//	@Failure		400		{object}	domain.ErrorResponse
//	@Router			/notifications/telegram [post]
func (h *NotificationHandler) SendTelegram(w http.ResponseWriter, r *http.Request) {
	var req domain.TelegramNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid user_id", "BAD_REQUEST")
		return
	}

	meta := req.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["chat_id"] = req.ChatID
	if req.ParseMode != "" {
		meta["parse_mode"] = req.ParseMode
	}

	n, err := h.repo.Create(r.Context(), userID, domain.TypeSystemAlert, domain.ChannelTelegram,
		"Telegram Notification", req.Message, nil, meta)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not queue telegram", "INTERNAL")
		return
	}

	middleware.WriteJSON(w, http.StatusAccepted, domain.InternalEventResponse{
		NotificationID: n.ID,
		Message:        "telegram message queued for delivery",
	})
}

// ─── Admin ────────────────────────────────────────────────────────────────────

// AdminStats godoc
//
//	@Summary		Notification service statistics
//	@Description	Returns aggregate stats. Requires ADMIN role JWT.
//	@Tags			admin
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.AdminStatsResponse
//	@Failure		403	{object}	domain.ErrorResponse
//	@Router			/admin/notifications/stats [get]
func (h *NotificationHandler) AdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	total, _ := h.repo.TotalNotifications(ctx)
	unread, _ := h.repo.TotalUnread(ctx)
	byType, _ := h.repo.CountByColumn(ctx, "type")
	byChannel, _ := h.repo.CountByColumn(ctx, "channel")
	byStatus, _ := h.repo.CountByColumn(ctx, "status")
	pushCount, _ := h.repo.CountPushSubscriptions(ctx)
	otpCount, _ := h.repo.CountPendingOTPs(ctx)
	outboxCount, _ := h.repo.CountPendingOutbox(ctx)

	middleware.WriteJSON(w, http.StatusOK, domain.AdminStatsResponse{
		TotalNotifications: total,
		UnreadTotal:        unread,
		ByType:             byType,
		ByChannel:          byChannel,
		ByStatus:           byStatus,
		PushSubscriptions:  pushCount,
		OTPPending:         otpCount,
		OutboxPending:      outboxCount,
		GeneratedAt:        time.Now().UTC(),
	})
}
