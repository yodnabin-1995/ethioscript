package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ethioscript/document-service/internal/domain"
	"github.com/ethioscript/document-service/internal/usecase"
	"go.uber.org/zap"
)

// AssetHandler handles asset upload initialisation.
type AssetHandler struct {
	uc      *usecase.DocumentUseCase
	storage usecase.StorageClient
	bucket  string
	log     *zap.Logger
}

// NewAssetHandler constructs an AssetHandler.
func NewAssetHandler(uc *usecase.DocumentUseCase, storage usecase.StorageClient, bucket string, log *zap.Logger) *AssetHandler {
	return &AssetHandler{uc: uc, storage: storage, bucket: bucket, log: log}
}

// InitUpload handles POST /assets
func (h *AssetHandler) InitUpload(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := extractIdentity(w, r)
	if !ok {
		return
	}

	var req domain.InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	resp, err := h.uc.InitUpload(r.Context(), tenantID, userID, req, h.storage, h.bucket)
	if err != nil {
		h.log.Error("init upload failed", zap.Error(err))
		writeError(w, r, http.StatusBadRequest, err.Error(), "UPLOAD_INIT_FAILED")
		return
	}
	writeJSON(w, r, http.StatusCreated, resp)
}
