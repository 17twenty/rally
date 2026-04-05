package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/queue"
)

// SlackHandler handles inbound Slack webhook events.
type SlackHandler struct {
	DB *db.DB
}

// slackEventPayload is the top-level structure Slack sends to the events endpoint.
type slackEventPayload struct {
	Token     string `json:"token"`
	TeamID    string `json:"team_id"`
	Type      string `json:"type"`
	Challenge string `json:"challenge,omitempty"`

	Event *slackInnerEvent `json:"event,omitempty"`
}

// slackInnerEvent holds the inner event object for event_callback payloads.
// UserRaw uses json.RawMessage because the "user" field is a string ID for most
// events but a full user object for team_join events.
type slackInnerEvent struct {
	Type     string          `json:"type"`
	Channel  string          `json:"channel"`
	UserRaw  json.RawMessage `json:"user"`
	Text     string          `json:"text"`
	TS       string          `json:"ts"`
	ThreadTS string          `json:"thread_ts"`
	Item     *slackReactionItem `json:"item,omitempty"`
	Reaction string          `json:"reaction,omitempty"`
}

// userID extracts the Slack user ID regardless of whether the field is a plain
// string (most events) or a nested user object (team_join).
func (e *slackInnerEvent) userID() string {
	if len(e.UserRaw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(e.UserRaw, &s) == nil {
		return s
	}
	var obj struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(e.UserRaw, &obj) == nil {
		return obj.ID
	}
	return ""
}

// realName extracts the human-readable name from a team_join user object.
func (e *slackInnerEvent) realName() string {
	var obj struct {
		Name    string `json:"name"`
		Profile struct {
			RealName string `json:"real_name"`
		} `json:"profile"`
	}
	if json.Unmarshal(e.UserRaw, &obj) == nil {
		if obj.Profile.RealName != "" {
			return obj.Profile.RealName
		}
		return obj.Name
	}
	return ""
}

type slackReactionItem struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

// Events handles POST /slack/events.
func (h *SlackHandler) Events(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if !verifySlackSignature(r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload slackEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Handle url_verification challenge.
	if payload.Type == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload.Challenge})
		return
	}

	// Return 200 immediately per Slack requirement (<3s).
	w.WriteHeader(http.StatusOK)

	if payload.Type != "event_callback" || payload.Event == nil {
		return
	}

	evt := payload.Event
	switch evt.Type {
	case "message", "app_mention", "reaction_added", "team_join":
		// ok, supported
	default:
		return
	}

	channel := evt.Channel
	userID := evt.userID()
	ts := evt.TS
	threadTS := evt.ThreadTS

	// For reaction_added events, pull channel/ts from the item.
	if evt.Type == "reaction_added" && evt.Item != nil {
		channel = evt.Item.Channel
		ts = evt.Item.TS
	}

	payloadMap := map[string]any{
		"team_id": payload.TeamID,
		"event":   evt,
	}

	if h.DB != nil {
		// Resolve company_id: single-tenant fallback to first active company.
		var companyID string
		_ = h.DB.Pool.QueryRow(r.Context(),
			`SELECT id FROM companies WHERE status IN ('active','pending') ORDER BY created_at LIMIT 1`,
		).Scan(&companyID)

		if err := h.insertSlackEvent(r, evt.Type, channel, userID, threadTS, ts, companyID, payloadMap); err != nil {
			log.Printf("slack: insert event: %v", err)
		}

		if queue.Client != nil {
			switch evt.Type {
			case "team_join":
				if _, err := queue.Client.Insert(r.Context(), queue.MemberJoinJobArgs{
					CompanyID:   companyID,
					SlackUserID: userID,
					RealName:    evt.realName(),
				}, nil); err != nil {
					log.Printf("slack: enqueue member_join: %v", err)
				}
			case "message", "app_mention":
				eventID := newID()
				if _, err := queue.Client.Insert(r.Context(), queue.SlackEventJobArgs{
					SlackEventID: eventID,
					CompanyID:    companyID,
				}, nil); err != nil {
					log.Printf("slack: enqueue slack_event: %v", err)
				}
			}
		}
	}

	log.Printf("slack: received %s event channel=%s user=%s ts=%s", evt.Type, channel, userID, ts)
}

func (h *SlackHandler) insertSlackEvent(r *http.Request, eventType, channel, userID, threadTS, messageTS, companyID string, payload map[string]any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	id := newID()
	_, err = h.DB.Pool.Exec(r.Context(),
		`INSERT INTO slack_events (id, company_id, event_type, channel, user_id, thread_ts, message_ts, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, companyID, eventType, channel, userID, threadTS, messageTS, payloadJSON,
	)
	return err
}

// verifySlackSignature verifies the X-Slack-Signature header using HMAC-SHA256.
// See https://api.slack.com/authentication/verifying-requests-from-slack
func verifySlackSignature(r *http.Request, body []byte) bool {
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if signingSecret == "" {
		// No secret configured — allow in dev, warn loudly.
		log.Println("WARNING: SLACK_SIGNING_SECRET not set, skipping signature verification")
		return true
	}

	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	slackSig := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || slackSig == "" {
		return false
	}

	// Reject requests older than 5 minutes to prevent replay attacks.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if abs(time.Now().Unix()-ts) > 300 {
		return false
	}

	baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(baseString))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(slackSig))
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
