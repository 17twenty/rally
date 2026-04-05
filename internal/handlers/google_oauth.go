package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/17twenty/rally/internal/vault"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleOAuthHandler manages the Google OAuth2 flow for AE credentials.
type GoogleOAuthHandler struct {
	Vault *vault.CredentialVault
}

var googleOAuthScopes = []string{
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/documents",
	"https://www.googleapis.com/auth/drive.file",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/calendar.events",
}

func (h *GoogleOAuthHandler) oauthConfig(r *http.Request) *oauth2.Config {
	redirectURI := os.Getenv("GOOGLE_REDIRECT_URI")
	if redirectURI == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		redirectURI = fmt.Sprintf("%s://%s/oauth/google/callback", scheme, r.Host)
	}
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes:       googleOAuthScopes,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURI,
	}
}

// Authorize handles GET /oauth/google — builds the consent URL and redirects.
// Query param: employee_id (required).
func (h *GoogleOAuthHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	employeeID := r.URL.Query().Get("employee_id")
	if employeeID == "" {
		http.Error(w, "employee_id is required", http.StatusBadRequest)
		return
	}

	// Encode employee_id as base64 JSON in the state parameter.
	stateData, _ := json.Marshal(map[string]string{"employee_id": employeeID})
	state := base64.URLEncoding.EncodeToString(stateData)

	cfg := h.oauthConfig(r)
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles GET /oauth/google/callback — exchanges the code and stores the token in vault.
func (h *GoogleOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
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

	// Decode state to extract employee_id.
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

	cfg := h.oauthConfig(r)
	tok, err := cfg.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if h.Vault != nil {
		tokenJSON, _ := json.Marshal(tok)
		if storeErr := h.Vault.Store(r.Context(), "", employeeID, "google_workspace",
			string(tokenJSON), "oauth2", googleOAuthScopes); storeErr != nil {
			http.Error(w, "failed to store token: "+storeErr.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/credentials?success=google_workspace", http.StatusFound)
}
