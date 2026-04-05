package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/17twenty/rally/internal/vault"
)

// FigmaOAuthHandler manages the Figma OAuth2 flow for AE credentials.
type FigmaOAuthHandler struct {
	Vault *vault.CredentialVault
}

func (h *FigmaOAuthHandler) redirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/oauth/figma/callback", scheme, r.Host)
}

// Authorize handles GET /oauth/figma — builds Figma OAuth URL and redirects.
// Query param: employee_id (required).
func (h *FigmaOAuthHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	employeeID := r.URL.Query().Get("employee_id")
	if employeeID == "" {
		http.Error(w, "employee_id is required", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("FIGMA_CLIENT_ID")
	if clientID == "" {
		http.Error(w, "FIGMA_CLIENT_ID not configured", http.StatusServiceUnavailable)
		return
	}

	// Encode employee_id in state parameter.
	stateData, _ := json.Marshal(map[string]string{"employee_id": employeeID})
	state := base64.URLEncoding.EncodeToString(stateData)

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", h.redirectURI(r))
	params.Set("scope", "file_read")
	params.Set("state", state)
	params.Set("response_type", "code")

	authURL := "https://www.figma.com/oauth?" + params.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles GET /oauth/figma/callback — exchanges code for token and stores in vault.
func (h *FigmaOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	errParam := r.URL.Query().Get("error")
	if errParam != "" {
		http.Error(w, "oauth error: "+errParam, http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}

	stateParam := r.URL.Query().Get("state")
	stateBytes, err := base64.URLEncoding.DecodeString(stateParam)
	if err != nil {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}
	var stateData map[string]string
	if err := json.Unmarshal(stateBytes, &stateData); err != nil {
		http.Error(w, "invalid state data", http.StatusBadRequest)
		return
	}
	employeeID := stateData["employee_id"]
	if employeeID == "" {
		http.Error(w, "missing employee_id in state", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("FIGMA_CLIENT_ID")
	clientSecret := os.Getenv("FIGMA_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		http.Error(w, "FIGMA_CLIENT_ID or FIGMA_CLIENT_SECRET not configured", http.StatusServiceUnavailable)
		return
	}

	// Exchange code for token via Figma OAuth token endpoint.
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", h.redirectURI(r))
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")

	resp, err := http.Post(
		"https://www.figma.com/api/oauth/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		http.Error(w, "token exchange request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read token response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "token exchange failed: "+string(body), http.StatusInternalServerError)
		return
	}

	var tokenResp map[string]any
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		http.Error(w, "failed to parse token response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	accessToken, _ := tokenResp["access_token"].(string)
	if accessToken == "" {
		http.Error(w, "no access_token in response", http.StatusInternalServerError)
		return
	}

	if h.Vault != nil {
		if storeErr := h.Vault.Store(r.Context(), "", employeeID, "figma",
			accessToken, "oauth2", []string{"file_read"}); storeErr != nil {
			http.Error(w, "failed to store token: "+storeErr.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/credentials?success=figma", http.StatusFound)
}
