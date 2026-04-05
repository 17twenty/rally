package domain

import "fmt"

// FormatSlackMessage formats a SlackMessage per SLACK_NOTES.md §6.1.
func FormatSlackMessage(msg SlackMessage) string {
	switch msg.Type {
	case SlackMessageIntent:
		return fmt.Sprintf("[%s]\nIntent: %s", msg.AuthorRole, msg.Body)
	case SlackMessageUpdate:
		return fmt.Sprintf("[%s]\nUpdate: %s", msg.AuthorRole, msg.Body)
	case SlackMessageBlocker:
		if msg.Target != "" {
			return fmt.Sprintf("[%s]\nBlocker: %s\n@%s requesting guidance", msg.AuthorRole, msg.Body, msg.Target)
		}
		return fmt.Sprintf("[%s]\nBlocker: %s", msg.AuthorRole, msg.Body)
	case SlackMessageRequest:
		if msg.Target != "" {
			return fmt.Sprintf("[%s → %s]\nRequest: %s", msg.AuthorRole, msg.Target, msg.Body)
		}
		return fmt.Sprintf("[%s]\nRequest: %s", msg.AuthorRole, msg.Body)
	case SlackMessageDecision:
		return fmt.Sprintf("[%s]\nDecision: %s", msg.AuthorRole, msg.Body)
	default:
		return fmt.Sprintf("[%s]\n%s", msg.AuthorRole, msg.Body)
	}
}
