package handler

import (
	"net/http"

	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"go.uber.org/zap"
)

const maxPhotoSize = 5 << 20 // 5 MB

// PhotoHandler handles profile photo uploads.
type PhotoHandler struct {
	uc            *usecase.AccountUseCase
	uploader      usecase.StorageUploader
	minioEndpoint string
	log           *zap.Logger
}

// NewPhotoHandler constructs a PhotoHandler.
func NewPhotoHandler(
	uc *usecase.AccountUseCase,
	uploader usecase.StorageUploader,
	minioEndpoint string,
	log *zap.Logger,
) *PhotoHandler {
	return &PhotoHandler{uc: uc, uploader: uploader, minioEndpoint: minioEndpoint, log: log}
}

// UploadPhoto godoc
//
//	@Summary     Upload profile photo
//	@Description Uploads a JPEG/PNG/WebP photo (max 5 MB) as the authenticated user's avatar.
//	             Send as multipart/form-data with field name "photo".
//	@Tags        auth
//	@Security    BearerAuth
//	@Accept      multipart/form-data
//	@Produce     json
//	@Param       photo formData file true "Profile photo (JPEG/PNG/WebP, max 5 MB)"
//	@Success     200 {object} map[string]string "photo_url"
//	@Failure     400 {object} domain.ErrorResponse
//	@Failure     413 {object} domain.ErrorResponse
//	@Router      /auth/me/photo [post]
func (h *PhotoHandler) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		middleware.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
		return
	}

	// Enforce max size before parsing
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoSize)
	if err := r.ParseMultipartForm(maxPhotoSize); err != nil {
		middleware.WriteError(w, r, http.StatusRequestEntityTooLarge, "photo must be ≤ 5 MB", "TOO_LARGE")
		return
	}

	file, header, err := r.FormFile("photo")
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "missing 'photo' field in form", "BAD_REQUEST")
		return
	}
	defer file.Close()

	// Validate MIME type
	contentType := header.Header.Get("Content-Type")
	allowed := map[string]bool{
		"image/jpeg": true,
		"image/jpg":  true,
		"image/png":  true,
		"image/webp": true,
		"image/gif":  true,
	}
	if !allowed[contentType] {
		middleware.WriteError(w, r, http.StatusBadRequest,
			"unsupported image type; allowed: jpeg, png, webp, gif", "INVALID_MIME")
		return
	}

	photoURL, err := h.uc.UploadProfilePhoto(
		r.Context(),
		claims.UserID,
		file,
		header.Size,
		contentType,
		h.uploader,
		h.minioEndpoint,
	)
	if err != nil {
		h.log.Error("profile photo upload failed", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "upload failed", "UPLOAD_FAILED")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]string{"photo_url": photoURL})
}
