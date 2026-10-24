package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ethioscript/account-service/internal/config"
	"github.com/ethioscript/account-service/internal/domain"
	"github.com/ethioscript/account-service/internal/middleware"
	"github.com/ethioscript/account-service/internal/usecase"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleHandler handles Google OAuth2 sign-in.
type GoogleHandler struct {
	uc     *usecase.AccountUseCase
	cfg    config.GoogleOAuthConfig
	oauth2 *oauth2.Config
	log    *zap.Logger
}

const googleOAuthState = "ethioscript-oauth-state" // stateless MVP; use Redis for CSRF in production

// NewGoogleHandler constructs a GoogleHandler.
func NewGoogleHandler(uc *usecase.AccountUseCase, cfg config.GoogleOAuthConfig, log *zap.Logger) *GoogleHandler {
	var o2cfg *oauth2.Config
	if cfg.Enabled {
		o2cfg = &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}
	}
	return &GoogleHandler{uc: uc, cfg: cfg, oauth2: o2cfg, log: log}
}

// Initiate godoc
//
//	@Summary     Initiate Google Sign-In
//	@Description Redirects the browser to Google's OAuth2 consent screen
//	@Tags        auth
//	@Success     302 "Redirect to Google"
//	@Router      /auth/google [get]
func (h *GoogleHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled || h.oauth2 == nil {
		middleware.WriteError(w, r, http.StatusNotImplemented,
			"Google sign-in is not configured (set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET)", "NOT_CONFIGURED")
		return
	}
	url := h.oauth2.AuthCodeURL(googleOAuthState, oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback godoc
//
//	@Summary     Google OAuth2 callback
//	@Description Exchanges the OAuth code for user info and returns EthioScript JWT tokens
//	@Tags        auth
//	@Produce     json
//	@Success     200 {object} domain.TokenResponse
//	@Failure     400 {object} domain.ErrorResponse
//	@Router      /auth/google/callback [get]
func (h *GoogleHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled || h.oauth2 == nil {
		middleware.WriteError(w, r, http.StatusNotImplemented, "Google sign-in is not configured", "NOT_CONFIGURED")
		return
	}

	if r.URL.Query().Get("state") != googleOAuthState {
		middleware.WriteError(w, r, http.StatusBadRequest, "invalid OAuth state", "INVALID_STATE")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		middleware.WriteError(w, r, http.StatusBadRequest, "missing authorization code", "MISSING_CODE")
		return
	}

	// Exchange code for token
	token, err := h.oauth2.Exchange(context.Background(), code)
	if err != nil {
		h.log.Error("google oauth exchange failed", zap.Error(err))
		middleware.WriteError(w, r, http.StatusBadRequest, "google auth failed", "OAUTH_FAILED")
		return
	}

	// Fetch Google user info
	info, err := fetchGoogleUserInfo(token.AccessToken)
	if err != nil {
		h.log.Error("google userinfo fetch failed", zap.Error(err))
		middleware.WriteError(w, r, http.StatusInternalServerError, "could not fetch user info", "USERINFO_FAILED")
		return
	}

	// Upsert user and issue EthioScript tokens
	resp, err := h.uc.LoginWithGoogle(r.Context(), *info, "default")
	if err != nil {
		writeUseCaseError(w, r, err)
		return
	}
	middleware.WriteJSON(w, http.StatusOK, resp)
}

// fetchGoogleUserInfo calls the Google userinfo endpoint.
func fetchGoogleUserInfo(accessToken string) (*domain.GoogleUserInfo, error) {
	req, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo non-200: %s %s", resp.Status, string(body))
	}

	var info domain.GoogleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("userinfo decode: %w", err)
	}
	if info.Email == "" || info.GoogleID == "" {
		return nil, fmt.Errorf("incomplete google user info")
	}
	return &info, nil
}
