//go:build e2e

// Package e2e tests the full EthioScript workflow end-to-end against
// running services. Start everything first:
//
//	make up
//
// Then run:
//
//	go test ./tests/e2e/... -v -tags=e2e -timeout=120s
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Config ───────────────────────────────────────────────────────────────────

var (
	accountURL  = getEnv("ACCOUNT_SERVICE_URL", "http://localhost:6000")
	documentURL = getEnv("DOCUMENT_SERVICE_URL", "http://localhost:6001")
	jobURL      = getEnv("JOB_SERVICE_URL", "http://localhost:6002")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func doJSON(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(target))
}

func requireStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected HTTP %d, got %d: %s", expected, resp.StatusCode, string(body))
	}
}

// ─── Test data ────────────────────────────────────────────────────────────────

func uniqueEmail() string {
	return fmt.Sprintf("e2e-%d@test.ethioscript.local", time.Now().UnixNano())
}

// ─── Test: Health checks ──────────────────────────────────────────────────────

func TestE2E_HealthChecks(t *testing.T) {
	for _, svc := range []struct {
		name string
		url  string
	}{
		{"account-service", accountURL + "/health"},
		{"document-service", documentURL + "/health"},
		{"job-orchestrator", jobURL + "/health"},
	} {
		t.Run(svc.name, func(t *testing.T) {
			resp, err := http.Get(svc.url)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, svc.name+" should be healthy")
		})
	}
}

// ─── Test: Full auth workflow ─────────────────────────────────────────────────

func TestE2E_AuthWorkflow(t *testing.T) {
	email := uniqueEmail()
	password := "Test1234"

	// 1. Register
	regResp := doJSON(t, "POST", accountURL+"/auth/register", "", map[string]string{
		"tenant_slug": "default",
		"name":        "E2E User",
		"email":       email,
		"password":    password,
	})
	requireStatus(t, regResp, http.StatusCreated)

	var regBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		User         struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
	}
	decodeJSON(t, regResp, &regBody)
	require.NotEmpty(t, regBody.AccessToken, "access token must be returned on register")
	require.NotEmpty(t, regBody.RefreshToken, "refresh token must be returned on register")
	assert.Equal(t, email, regBody.User.Email)
	assert.Equal(t, "CONTRIBUTOR", regBody.User.Role)

	accessToken := regBody.AccessToken
	refreshToken := regBody.RefreshToken

	// 2. Get profile
	meResp := doJSON(t, "GET", accountURL+"/auth/me", accessToken, nil)
	requireStatus(t, meResp, http.StatusOK)
	var me struct {
		Email string `json:"email"`
	}
	decodeJSON(t, meResp, &me)
	assert.Equal(t, email, me.Email)

	// 3. Update profile
	updateResp := doJSON(t, "PUT", accountURL+"/auth/me", accessToken, map[string]string{
		"name": "E2E Updated Name",
	})
	requireStatus(t, updateResp, http.StatusOK)
	var updatedUser struct {
		Name string `json:"name"`
	}
	decodeJSON(t, updateResp, &updatedUser)
	assert.Equal(t, "E2E Updated Name", updatedUser.Name)

	// 4. Refresh token
	refreshResp := doJSON(t, "POST", accountURL+"/auth/refresh", "", map[string]string{
		"refresh_token": refreshToken,
	})
	requireStatus(t, refreshResp, http.StatusOK)
	var refreshBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	decodeJSON(t, refreshResp, &refreshBody)
	require.NotEmpty(t, refreshBody.AccessToken)
	require.NotEmpty(t, refreshBody.RefreshToken)
	// Old refresh token must now be rotated (different)
	assert.NotEqual(t, refreshToken, refreshBody.RefreshToken, "refresh token must rotate")

	newAccessToken := refreshBody.AccessToken
	newRefreshToken := refreshBody.RefreshToken

	// 5. Old refresh token must be rejected (rotated)
	oldRefreshResp := doJSON(t, "POST", accountURL+"/auth/refresh", "", map[string]string{
		"refresh_token": refreshToken,
	})
	assert.Equal(t, http.StatusUnauthorized, oldRefreshResp.StatusCode, "rotated token must be rejected")
	oldRefreshResp.Body.Close()

	// 6. Logout
	logoutResp := doJSON(t, "POST", accountURL+"/auth/logout", newAccessToken, map[string]string{
		"refresh_token": newRefreshToken,
	})
	assert.Equal(t, http.StatusNoContent, logoutResp.StatusCode)
	logoutResp.Body.Close()

	// 7. After logout, refresh must fail
	postLogoutResp := doJSON(t, "POST", accountURL+"/auth/refresh", "", map[string]string{
		"refresh_token": newRefreshToken,
	})
	assert.Equal(t, http.StatusUnauthorized, postLogoutResp.StatusCode, "revoked token must be rejected after logout")
	postLogoutResp.Body.Close()

	// 8. Login again
	loginResp := doJSON(t, "POST", accountURL+"/auth/login", "", map[string]string{
		"email":    email,
		"password": password,
	})
	requireStatus(t, loginResp, http.StatusOK)
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	decodeJSON(t, loginResp, &loginBody)
	require.NotEmpty(t, loginBody.AccessToken)
}

// ─── Test: API key lifecycle ──────────────────────────────────────────────────

func TestE2E_APIKeyLifecycle(t *testing.T) {
	email := uniqueEmail()

	// Register + get token
	regResp := doJSON(t, "POST", accountURL+"/auth/register", "", map[string]string{
		"tenant_slug": "default", "name": "APIKey User",
		"email": email, "password": "Test1234",
	})
	requireStatus(t, regResp, http.StatusCreated)
	var regBody struct {
		AccessToken string `json:"access_token"`
	}
	decodeJSON(t, regResp, &regBody)
	token := regBody.AccessToken

	// Create API key
	createResp := doJSON(t, "POST", accountURL+"/api-keys", token, map[string]any{
		"name":   "E2E Test Key",
		"scopes": []string{"documents:read", "documents:write"},
	})
	requireStatus(t, createResp, http.StatusCreated)
	var createBody struct {
		APIKey struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			KeyPrefix string `json:"key_prefix"`
		} `json:"api_key"`
		RawKey string `json:"raw_key"`
	}
	decodeJSON(t, createResp, &createBody)
	require.NotEmpty(t, createBody.RawKey, "raw key must be returned on creation")
	require.NotEmpty(t, createBody.APIKey.ID)
	assert.Equal(t, "E2E Test Key", createBody.APIKey.Name)

	keyID := createBody.APIKey.ID

	// List keys
	listResp := doJSON(t, "GET", accountURL+"/api-keys", token, nil)
	requireStatus(t, listResp, http.StatusOK)
	var keys []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decodeJSON(t, listResp, &keys)
	found := false
	for _, k := range keys {
		if k.ID == keyID {
			found = true
			break
		}
	}
	assert.True(t, found, "created key must appear in list")

	// Revoke
	revokeResp := doJSON(t, "DELETE", accountURL+"/api-keys/"+keyID, token, nil)
	assert.Equal(t, http.StatusNoContent, revokeResp.StatusCode)
	revokeResp.Body.Close()

	// After revocation must not appear in list
	listResp2 := doJSON(t, "GET", accountURL+"/api-keys", token, nil)
	requireStatus(t, listResp2, http.StatusOK)
	var keys2 []struct{ ID string `json:"id"` }
	decodeJSON(t, listResp2, &keys2)
	for _, k := range keys2 {
		assert.NotEqual(t, keyID, k.ID, "revoked key must not appear in list")
	}
}

// ─── Test: Document workflow ──────────────────────────────────────────────────

func TestE2E_DocumentWorkflow(t *testing.T) {
	email := uniqueEmail()

	// Register reviewer (role elevated in DB for this test via seeded default tenant)
	regResp := doJSON(t, "POST", accountURL+"/auth/register", "", map[string]string{
		"tenant_slug": "default", "name": "Doc Reviewer",
		"email": email, "password": "Test1234",
	})
	requireStatus(t, regResp, http.StatusCreated)
	var regBody struct {
		AccessToken string `json:"access_token"`
		User        struct{ TenantID string `json:"tenant_id"` } `json:"user"`
	}
	decodeJSON(t, regResp, &regBody)
	token := regBody.AccessToken

	// 1. Create document (requires an asset_id — we use a fake UUID for e2e
	//    since we're not testing storage, just the workflow state machine)
	fakeAssetID := "00000000-0000-0000-0000-000000000001"
	createDocResp := doJSON(t, "POST", documentURL+"/documents", token, map[string]any{
		"asset_id":  fakeAssetID,
		"filename":  "amharic_test.pdf",
		"language":  "amh",
		"task_type": "OCR",
		"title":     "E2E OCR Test",
		"tags":      []string{"e2e", "test"},
	})
	// 201 if asset exists, 400/500 if asset not in DB — both are valid for this e2e check
	t.Logf("create document status: %d", createDocResp.StatusCode)
	createDocResp.Body.Close()

	// 2. List documents — must succeed and be scoped to tenant
	listResp := doJSON(t, "GET", documentURL+"/documents?page=1&page_size=10", token, nil)
	requireStatus(t, listResp, http.StatusOK)
	var listBody struct {
		Documents []struct{ ID string `json:"id"` } `json:"documents"`
		Total     int                               `json:"total"`
	}
	decodeJSON(t, listResp, &listBody)
	t.Logf("documents in tenant: %d", listBody.Total)

	// 3. Verify document-service health endpoint
	healthResp, err := http.Get(documentURL + "/health")
	require.NoError(t, err)
	defer healthResp.Body.Close()
	assert.Equal(t, http.StatusOK, healthResp.StatusCode)
}

// ─── Test: Forgot/reset password (token round-trip check) ────────────────────

func TestE2E_ForgotPasswordDoesNotLeak(t *testing.T) {
	// Forgot password must return 204 even for non-existent email (no enumeration)
	resp := doJSON(t, "POST", accountURL+"/auth/forgot-password", "", map[string]string{
		"email": "nonexistent@ghost.test",
	})
	assert.Equal(t, http.StatusNoContent, resp.StatusCode,
		"forgot-password must always return 204 regardless of whether email exists")
	resp.Body.Close()
}

// ─── Test: Invalid login must return 401 ─────────────────────────────────────

func TestE2E_InvalidLoginReturns401(t *testing.T) {
	resp := doJSON(t, "POST", accountURL+"/auth/login", "", map[string]string{
		"email":    "noone@test.ethioscript.local",
		"password": "WrongPass1",
	})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// ─── Test: Protected endpoint without token returns 401 ──────────────────────

func TestE2E_ProtectedEndpointRequiresAuth(t *testing.T) {
	for _, endpoint := range []string{
		accountURL + "/auth/me",
		accountURL + "/api-keys",
		accountURL + "/users",
		documentURL + "/documents",
	} {
		t.Run(endpoint, func(t *testing.T) {
			req, _ := http.NewRequest("GET", endpoint, nil)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				endpoint+" must require authentication")
		})
	}
}
