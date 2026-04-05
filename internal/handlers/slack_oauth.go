package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/vault"
)

func (h *SlackOAuthHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// OAuthScopes lists the required Slack OAuth scopes per SLACK_NOTES §3.2.
var OAuthScopes = []string{
	"chat:write",
	"chat:write.public",
	"channels:read",
	"channels:join",
	"channels:history",
	"groups:read",
	"groups:history",
	"im:read",
	"im:write",
	"im:history",
	"mpim:read",
	"users:read",
	"users:read.email",
	"app_mentions:read",
	"reactions:write",
}

const slackOAuthAccessURL = "https://slack.com/api/oauth.v2.access"

// SlackOAuthHandler handles Slack OAuth installation flow.
type SlackOAuthHandler struct {
	DB          *db.DB
	Vault       *vault.CredentialVault
	SlackClient **slack.SlackClient // pointer-to-pointer so we can hot-swap the client
}

// Install handles GET /slack/install — renders install page with OAuth button.
func (h *SlackOAuthHandler) Install(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("SLACK_CLIENT_ID")
	redirectURI := os.Getenv("SLACK_REDIRECT_URI")
	if redirectURI == "" {
		redirectURI = "http://localhost:8432/slack/oauth/callback"
	}
	scopes := strings.Join(OAuthScopes, ",")

	authURL := fmt.Sprintf(
		"https://slack.com/oauth/v2/authorize?client_id=%s&scope=%s&redirect_uri=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(scopes),
		url.QueryEscape(redirectURI),
	)

	tmpl := template.Must(template.New("install").Parse(installPageHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, map[string]string{
		"AuthURL": authURL,
		"Scopes":  strings.Join(OAuthScopes, ", "),
	})
}

// OAuthCallback handles GET /slack/oauth/callback.
func (h *SlackOAuthHandler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error")
		http.Error(w, "oauth error: "+errMsg, http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("SLACK_CLIENT_ID")
	clientSecret := os.Getenv("SLACK_CLIENT_SECRET")
	redirectURI := os.Getenv("SLACK_REDIRECT_URI")
	if redirectURI == "" {
		redirectURI = "http://localhost:8432/slack/oauth/callback"
	}

	token, err := exchangeOAuthCode(r, code, clientID, clientSecret, redirectURI)
	if err != nil {
		slog.Error("slack oauth: exchange failed", "err", err)
		http.Error(w, "oauth exchange failed", http.StatusInternalServerError)
		return
	}

	slog.Info("slack oauth: token received",
		"team_id", token.Team.ID,
		"team_name", token.Team.Name,
		"bot_user_id", token.BotUserID,
	)

	// Store the bot token in the vault keyed to the company.
	if h.Vault != nil && h.DB != nil {
		// Find the first company (setup flow creates one).
		var companyID string
		if companies, listErr := h.q().ListCompanies(r.Context()); listErr == nil && len(companies) > 0 {
			companyID = companies[len(companies)-1].ID // oldest first (ListCompanies is DESC, so last = oldest)
		}

		if companyID != "" {
			storeErr := h.Vault.Store(r.Context(), companyID, "rally-system", "slack",
				token.AccessToken, "oauth", OAuthScopes)
			if storeErr != nil {
				slog.Warn("slack oauth: failed to store token in vault", "err", storeErr)
			} else {
				slog.Info("slack oauth: token stored in vault", "company_id", companyID)
			}

			// Also store team metadata.
			_ = h.q().UpdateSlackTeam(r.Context(), dao.UpdateSlackTeamParams{
				ID:            companyID,
				SlackTeamID:   db.Ref(token.Team.ID),
				SlackTeamName: db.Ref(token.Team.Name),
			})
		}
	}

	// Hot-swap the SlackClient so AEs can use Slack immediately without restart.
	if h.SlackClient != nil {
		newClient := slack.NewClient(token.AccessToken)
		*h.SlackClient = newClient
		slog.Info("slack oauth: SlackClient hot-swapped")
	}

	// Redirect to setup/dashboard with success message.
	http.Redirect(w, r, "/?msg=Slack+connected+successfully", http.StatusSeeOther)
}

type oauthV2Response struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	BotUserID string `json:"bot_user_id"`
}

func exchangeOAuthCode(r *http.Request, code, clientID, clientSecret, redirectURI string) (*oauthV2Response, error) {
	params := url.Values{}
	params.Set("code", code)
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	if redirectURI != "" {
		params.Set("redirect_uri", redirectURI)
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, slackOAuthAccessURL,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post oauth.v2.access: %w", err)
	}
	defer resp.Body.Close()

	var result oauthV2Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("slack api error: %s", result.Error)
	}
	return &result, nil
}

const installPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Install Rally for Slack</title>
  <style>
    body { font-family: sans-serif; max-width: 600px; margin: 60px auto; text-align: center; }
    .scopes { text-align: left; background: #f5f5f5; padding: 16px; border-radius: 8px; font-size: 0.9em; }
    .btn { display: inline-block; margin-top: 24px; padding: 12px 24px; background: #4a154b;
           color: #fff; text-decoration: none; border-radius: 6px; font-size: 1.1em; }
    .btn:hover { background: #611f69; }
  </style>
</head>
<body>
  <h1>Add Rally to Slack</h1>
  <p>Rally needs the following scopes to operate as your AI executive team:</p>
  <div class="scopes"><code>{{.Scopes}}</code></div>
  <a class="btn" href="{{.AuthURL}}">
    <img src="https://platform.slack-edge.com/img/add_to_slack.png"
         alt="Add to Slack" height="40">
  </a>
</body>
</html>`
