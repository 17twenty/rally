package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const slackAPIBase = "https://slack.com/api"

// Channel represents a Slack channel.
type Channel struct {
	ID        string
	Name      string
	IsPrivate bool
}

// SlackUser represents a Slack workspace user.
type SlackUser struct {
	ID       string
	Name     string
	RealName string
	IsBot    bool
}

// SlackClient wraps HTTP calls to the Slack Web API.
type SlackClient struct {
	botToken   string
	httpClient *http.Client
}

// NewClient returns a new SlackClient authenticated with the given bot token.
func NewClient(botToken string) *SlackClient {
	return &SlackClient{
		botToken:   botToken,
		httpClient: &http.Client{},
	}
}

// apiResponse is the common envelope returned by all Slack API methods.
type apiResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *SlackClient) postJSON(ctx context.Context, method string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("slack: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPIBase+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %s: %w", method, err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("slack: decode response: %w", err)
	}
	return nil
}

func (c *SlackClient) getJSON(ctx context.Context, method string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		slackAPIBase+"/"+method, nil)
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %s: %w", method, err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("slack: decode response: %w", err)
	}
	return nil
}

// PostMessage sends text to a channel and returns the message timestamp.
func (c *SlackClient) PostMessage(ctx context.Context, channel, text string) (string, error) {
	var result struct {
		apiResponse
		TS string `json:"ts"`
	}
	err := c.postJSON(ctx, "chat.postMessage", map[string]string{
		"channel": channel,
		"text":    text,
	}, &result)
	if err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("slack: chat.postMessage: %s", result.Error)
	}
	return result.TS, nil
}

// PostMessageAsPersona prepends a [PersonaName] prefix per SLACK_NOTES §4.1.
func (c *SlackClient) PostMessageAsPersona(ctx context.Context, channel, text, personaName string) (string, error) {
	prefixed := fmt.Sprintf("[%s] %s", personaName, text)
	return c.PostMessage(ctx, channel, prefixed)
}

// ReplyInThread posts a message into an existing thread.
func (c *SlackClient) ReplyInThread(ctx context.Context, channel, threadTS, text string) (string, error) {
	var result struct {
		apiResponse
		TS string `json:"ts"`
	}
	err := c.postJSON(ctx, "chat.postMessage", map[string]string{
		"channel":   channel,
		"text":      text,
		"thread_ts": threadTS,
	}, &result)
	if err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("slack: chat.postMessage (thread): %s", result.Error)
	}
	return result.TS, nil
}

// JoinChannel joins the bot to a channel by ID.
func (c *SlackClient) JoinChannel(ctx context.Context, channelID string) error {
	var result apiResponse
	err := c.postJSON(ctx, "conversations.join", map[string]string{
		"channel": channelID,
	}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("slack: conversations.join: %s", result.Error)
	}
	return nil
}

// ListChannels returns all public (and private, if scoped) channels in the workspace.
func (c *SlackClient) ListChannels(ctx context.Context) ([]Channel, error) {
	var result struct {
		apiResponse
		Channels []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			IsPrivate bool   `json:"is_private"`
		} `json:"channels"`
	}
	if err := c.getJSON(ctx, "conversations.list", &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("slack: conversations.list: %s", result.Error)
	}
	channels := make([]Channel, len(result.Channels))
	for i, ch := range result.Channels {
		channels[i] = Channel{
			ID:        ch.ID,
			Name:      ch.Name,
			IsPrivate: ch.IsPrivate,
		}
	}
	return channels, nil
}

// ListUsers returns all users in the workspace.
func (c *SlackClient) ListUsers(ctx context.Context) ([]SlackUser, error) {
	var result struct {
		apiResponse
		Members []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			IsBot   bool   `json:"is_bot"`
			Profile struct {
				RealName string `json:"real_name"`
			} `json:"profile"`
		} `json:"members"`
	}
	if err := c.getJSON(ctx, "users.list", &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("slack: users.list: %s", result.Error)
	}
	users := make([]SlackUser, len(result.Members))
	for i, m := range result.Members {
		users[i] = SlackUser{
			ID:       m.ID,
			Name:     m.Name,
			RealName: m.Profile.RealName,
			IsBot:    m.IsBot,
		}
	}
	return users, nil
}

// GetChannelByName looks up a channel by name, returning nil if not found.
func (c *SlackClient) GetChannelByName(ctx context.Context, name string) (*Channel, error) {
	channels, err := c.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	for i := range channels {
		if channels[i].Name == name {
			return &channels[i], nil
		}
	}
	return nil, nil
}

// AddReaction adds an emoji reaction to a message.
func (c *SlackClient) AddReaction(ctx context.Context, channel, messageTS, emoji string) error {
	var result apiResponse
	err := c.postJSON(ctx, "reactions.add", map[string]string{
		"channel":   channel,
		"name":      emoji,
		"timestamp": messageTS,
	}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("slack: reactions.add: %s", result.Error)
	}
	return nil
}
