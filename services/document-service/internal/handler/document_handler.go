package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ethioscript/document-service/internal/domain"
	"github.com/ethioscript/document-service/internal/middleware"
	"github.com/ethioscript/document-service/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DocumentHandler handles HTTP requests for document operations.
type DocumentHandler struct {
	uc      *usecase.DocumentUseCase
	storage usecase.StorageClient
	log     *zap.Logger
}

// NewDocumentHandler constructs a DocumentHandler.
func NewDocumentHandler(uc *usecase.DocumentUseCase, storage usecase.StorageClient, log *zap.Logger) *DocumentHandler {
	return &DocumentHandler{uc: uc, storage: storage, log: log}
}

// CreateDocument handles POST /documents
func (h *DocumentHandler) CreateDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}

	var req domain.CreateDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.AssetID == "" || req.Filename == "" {
		writeError(w, r, http.StatusBadRequest, "asset_id and filename are required", "VALIDATION_ERROR")
		return
	}

	doc, err := h.uc.CreateDocument(r.Context(), tenantID, userID, req)
	if err != nil {
		h.log.Error("create document failed", zap.Error(err))
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}
	writeJSON(w, r, http.StatusCreated, doc)
}

// GetDocument handles GET /documents/{id}
func (h *DocumentHandler) GetDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	doc, err := h.uc.GetDocument(r.Context(), tenantID, docID)
	if errors.Is(err, domain.ErrDocumentNotFound) {
		writeError(w, r, http.StatusNotFound, "document not found", "NOT_FOUND")
		return
	}
	if err != nil {
		h.log.Error("get document failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not retrieve document", "INTERNAL_ERROR")
		return
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// ListDocuments handles GET /documents
func (h *DocumentHandler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := extractIdentity(w, r)
	if !ok {
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))

	result, err := h.uc.ListDocuments(r.Context(), domain.ListDocumentsParams{
		TenantID: tenantID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		h.log.Error("list documents failed",
			zap.Error(err),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not list documents", "INTERNAL_ERROR")
		return
	}
	writeJSON(w, r, http.StatusOK, result)
}

// SaveDraft handles PUT /documents/{id}/result/draft
func (h *DocumentHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	var req domain.SaveDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.Text == "" {
		writeError(w, r, http.StatusBadRequest, "text is required", "VALIDATION_ERROR")
		return
	}

	if err := h.uc.SaveDraft(r.Context(), tenantID, docID, userID, req); err != nil {
		if errors.Is(err, domain.ErrDocumentNotEditable) {
			writeError(w, r, http.StatusConflict, "document is not in an editable state", "NOT_EDITABLE")
			return
		}
		h.log.Error("save draft failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not save draft", "INTERNAL_ERROR")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ApproveDocument handles POST /documents/{id}/approve
func (h *DocumentHandler) ApproveDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, reviewerID, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	if err := h.uc.ApproveDocument(r.Context(), tenantID, docID, reviewerID); err != nil {
		if errors.Is(err, domain.ErrDocumentNotFound) {
			writeError(w, r, http.StatusNotFound, "document not found", "NOT_FOUND")
			return
		}
		if errors.Is(err, domain.ErrInvalidTransition) {
			writeError(w, r, http.StatusConflict, "document cannot be approved in its current state", "INVALID_TRANSITION")
			return
		}
		h.log.Error("approve document failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not approve document", "INTERNAL_ERROR")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteDocument handles DELETE /documents/{id}
func (h *DocumentHandler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	if err := h.uc.SoftDeleteDocument(r.Context(), tenantID, docID, userID); err != nil {
		if errors.Is(err, domain.ErrDocumentNotFound) {
			writeError(w, r, http.StatusNotFound, "document not found", "NOT_FOUND")
			return
		}
		h.log.Error("delete document failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not delete document", "INTERNAL_ERROR")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Health handles GET /health
func (h *DocumentHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "service": "document-service"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func extractIdentity(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	tenantStr := r.Header.Get("X-Tenant-ID")
	userStr := r.Header.Get("X-User-ID")

	tenantID, err1 := uuid.Parse(tenantStr)
	userID, err2 := uuid.Parse(userStr)

	if err1 != nil || err2 != nil {
		writeError(w, r, http.StatusUnauthorized, "missing identity headers", "AUTH_REQUIRED")
		return uuid.UUID{}, uuid.UUID{}, false
	}
	return tenantID, userID, true
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg, code string) {
	writeJSON(w, r, status, map[string]string{"error": msg, "code": code})
}

// GetResult handles GET /documents/{id}/result
func (h *DocumentHandler) GetResult(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	rv, err := h.uc.GetResult(r.Context(), tenantID, docID)
	if errors.Is(err, domain.ErrDocumentNotFound) {
		writeError(w, r, http.StatusNotFound, "document not found", "NOT_FOUND")
		return
	}
	if errors.Is(err, domain.ErrNoResultFound) {
		writeError(w, r, http.StatusNotFound, "no result available yet", "NO_RESULT")
		return
	}
	if err != nil {
		h.log.Error("get result failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not retrieve result", "INTERNAL_ERROR")
		return
	}
	writeJSON(w, r, http.StatusOK, rv)
}

// RetryDocument handles POST /documents/{id}/retry
func (h *DocumentHandler) RetryDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	if err := h.uc.RetryDocument(r.Context(), tenantID, docID, userID); err != nil {
		if errors.Is(err, domain.ErrDocumentNotFound) {
			writeError(w, r, http.StatusNotFound, "document not found", "NOT_FOUND")
			return
		}
		if errors.Is(err, domain.ErrInvalidTransition) {
			writeError(w, r, http.StatusConflict, "document can only be retried when FAILED", "INVALID_TRANSITION")
			return
		}
		h.log.Error("retry document failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not retry document", "INTERNAL_ERROR")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ExportDocument handles POST /documents/{id}/exports
func (h *DocumentHandler) ExportDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid document id", "BAD_REQUEST")
		return
	}

	var req domain.ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to TXT if body missing/invalid
		req.Format = domain.ExportTXT
	}

	resp, err := h.uc.ExportDocument(r.Context(), tenantID, docID, userID, req, h.storage)
	if errors.Is(err, domain.ErrDocumentNotFound) || errors.Is(err, domain.ErrNoResultFound) {
		writeError(w, r, http.StatusNotFound, err.Error(), "NOT_FOUND")
		return
	}
	if err != nil {
		h.log.Error("export failed",
			zap.Error(err),
			zap.String("doc_id", docID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "export failed", "EXPORT_FAILED")
		return
	}
	writeJSON(w, r, http.StatusCreated, resp)
}

// ListDocumentsFiltered handles GET /documents with query filters
func (h *DocumentHandler) ListDocumentsFiltered(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := extractIdentity(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))

	params := domain.ListDocumentsParams{
		TenantID: tenantID,
		Page:     page,
		PageSize: pageSize,
	}

	if s := q.Get("status"); s != "" {
		st := domain.DocumentStatus(s)
		params.Status = &st
	}
	if l := q.Get("language"); l != "" {
		params.Language = &l
	}
	if t := q.Get("task_type"); t != "" {
		tt := domain.TaskType(t)
		params.TaskType = &tt
	}

	result, err := h.uc.ListDocumentsFiltered(r.Context(), params)
	if err != nil {
		h.log.Error("list documents filtered failed",
			zap.Error(err),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		writeError(w, r, http.StatusInternalServerError, "could not list documents", "INTERNAL_ERROR")
		return
	}
	writeJSON(w, r, http.StatusOK, result)
}
