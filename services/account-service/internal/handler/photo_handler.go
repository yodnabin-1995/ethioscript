package handler

import (
	"fmt"
	"net/http"

	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"go.uber.org/zap"
)

const (
	maxPhotoSize    = 5 << 20        // 5 MB — max file size shown to user
	maxRequestSize  = maxPhotoSize + (1 << 20) // 6 MB — reader limit includes multipart overhead
)

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

	// Use a larger reader limit to accommodate multipart overhead (~1 MB),
	// then check the actual file size separately.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	if err := r.ParseMultipartForm(maxRequestSize); err != nil {
		h.log.Warn("photo upload rejected — request too large",
			zap.Error(err),
			zap.String("user_id",  claims.UserID.String()),
			zap.String("trace_id", middleware.GetTraceID(r.Context())),
		)
		middleware.WriteError(w, r, http.StatusRequestEntityTooLarge, "photo must be ≤ 5 MB", "TOO_LARGE")
		return
	}

	file, header, err := r.FormFile("photo")
	if err != nil {
		middleware.WriteError(w, r, http.StatusBadRequest, "missing 'photo' field in form", "BAD_REQUEST")
		return
	}
	defer file.Close()

	// Enforce 5 MB on the actual file size (not the request envelope)
	if header.Size > maxPhotoSize {
		h.log.Warn("photo upload rejected — file too large",
			zap.Int64("size_bytes", header.Size),
			zap.String("user_id",   claims.UserID.String()),
			zap.String("trace_id",  middleware.GetTraceID(r.Context())),
		)
		middleware.WriteError(w, r, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("photo must be ≤ 5 MB (got %.1f MB)", float64(header.Size)/(1<<20)), "TOO_LARGE")
		return
	}

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
