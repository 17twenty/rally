package tools

import (
	"context"
	"fmt"

	"github.com/17twenty/rally/internal/slack"
)

// SlackTool handles Slack actions on behalf of an AE.
type SlackTool struct {
	Client *slack.SlackClient
}

// Execute dispatches a Slack action.
func (t *SlackTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	switch action {
	case "post_message":
		channel, _ := input["channel"].(string)
		text, _ := input["text"].(string)
		persona, _ := input["persona"].(string)
		if channel == "" || text == "" {
			return nil, fmt.Errorf("slack.post_message: channel and text required")
		}
		ts, err := t.Client.PostMessageAsPersona(ctx, channel, text, persona)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ts": ts}, nil

	case "reply_thread":
		channel, _ := input["channel"].(string)
		threadTS, _ := input["thread_ts"].(string)
		text, _ := input["text"].(string)
		persona, _ := input["persona"].(string)
		if channel == "" || threadTS == "" || text == "" {
			return nil, fmt.Errorf("slack.reply_thread: channel, thread_ts, and text required")
		}
		msg := text
		if persona != "" {
			msg = fmt.Sprintf("*%s:* %s", persona, text)
		}
		ts, err := t.Client.ReplyInThread(ctx, channel, threadTS, msg)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ts": ts}, nil

	case "list_channels":
		channels, err := t.Client.ListChannels(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]map[string]any, len(channels))
		for i, ch := range channels {
			result[i] = map[string]any{
				"id":         ch.ID,
				"name":       ch.Name,
				"is_private": ch.IsPrivate,
			}
		}
		return map[string]any{"channels": result}, nil

	case "read_channel":
		channel, _ := input["channel"].(string)
		if channel == "" {
			return nil, fmt.Errorf("slack.read_channel: channel required")
		}
		// Resolve channel name to ID if needed (e.g., "#all-rally-test" → "C0AQRPFU6HK").
		if channel[0] == '#' {
			ch, err := t.Client.GetChannelByName(ctx, channel[1:])
			if err != nil {
				return nil, fmt.Errorf("slack.read_channel: channel %s not found: %w", channel, err)
			}
			channel = ch.ID
		}
		limit := 20
		if l, ok := input["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		messages, err := t.Client.ReadChannel(ctx, channel, limit)
		if err != nil {
			return nil, err
		}
		result := make([]map[string]any, len(messages))
		for i, m := range messages {
			result[i] = map[string]any{
				"user": m.User, "text": m.Text, "ts": m.TS, "type": m.Type,
			}
		}
		return map[string]any{"messages": result, "count": len(result)}, nil

	case "read_thread":
		channel, _ := input["channel"].(string)
		threadTS, _ := input["thread_ts"].(string)
		if channel == "" || threadTS == "" {
			return nil, fmt.Errorf("slack.read_thread: channel and thread_ts required")
		}
		if channel[0] == '#' {
			ch, err := t.Client.GetChannelByName(ctx, channel[1:])
			if err != nil {
				return nil, fmt.Errorf("slack.read_thread: channel %s not found: %w", channel, err)
			}
			channel = ch.ID
		}
		limit := 20
		if l, ok := input["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		messages, err := t.Client.ReadThread(ctx, channel, threadTS, limit)
		if err != nil {
			return nil, err
		}
		result := make([]map[string]any, len(messages))
		for i, m := range messages {
			result[i] = map[string]any{
				"user": m.User, "text": m.Text, "ts": m.TS, "type": m.Type,
			}
		}
		return map[string]any{"messages": result, "count": len(result)}, nil

	case "add_reaction":
		channel, _ := input["channel"].(string)
		messageTS, _ := input["message_ts"].(string)
		emoji, _ := input["emoji"].(string)
		if channel == "" || messageTS == "" || emoji == "" {
			return nil, fmt.Errorf("slack.add_reaction: channel, message_ts, and emoji required")
		}
		if err := t.Client.AddReaction(ctx, channel, messageTS, emoji); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	default:
		return nil, fmt.Errorf("slack: unknown action %q", action)
	}
}
