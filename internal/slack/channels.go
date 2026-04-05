package slack

import (
	"context"
	"fmt"
)

// defaultChannels are the standard Rally channels per SLACK_NOTES §5.
var defaultChannels = []string{"general", "exec", "engineering", "product", "support"}

// EnsureDefaultChannels joins the bot to each default Rally channel.
// It uses Mode A (existing workspace) per SLACK_NOTES §2.1: it joins
// pre-existing channels and does not attempt to create them.
func EnsureDefaultChannels(ctx context.Context, client *SlackClient) error {
	for _, name := range defaultChannels {
		ch, err := client.GetChannelByName(ctx, name)
		if err != nil {
			return fmt.Errorf("ensure channels: look up #%s: %w", name, err)
		}
		if ch == nil {
			// Channel not found in workspace; skip (Mode A — we don't create channels).
			continue
		}
		if err := client.JoinChannel(ctx, ch.ID); err != nil {
			return fmt.Errorf("ensure channels: join #%s: %w", name, err)
		}
	}
	return nil
}
