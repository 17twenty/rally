package slack

import "fmt"

// BuildIntentMessage formats an intent message per SLACK_NOTES §6.1.
//
//	[Engineer-AE]
//	Intent: Investigating failing CI pipeline
//	Reason: Blocking current development work
func BuildIntentMessage(persona, intent, reason string) string {
	return fmt.Sprintf("[%s]\nIntent: %s\nReason: %s", persona, intent, reason)
}

// BuildUpdateMessage formats an update message per SLACK_NOTES §6.1.
//
//	[Engineer-AE]
//	Update: Identified issue in test configuration
//	Next: Preparing fix
func BuildUpdateMessage(persona, update, next string) string {
	return fmt.Sprintf("[%s]\nUpdate: %s\nNext: %s", persona, update, next)
}

// BuildBlockerMessage formats a blocker message per SLACK_NOTES §6.1.
//
//	[Engineer-AE]
//	Blocker: Unclear which test framework to standardize on
//	@CTO-AE requesting guidance
func BuildBlockerMessage(persona, blocker, target string) string {
	if target != "" {
		return fmt.Sprintf("[%s]\nBlocker: %s\n@%s requesting guidance", persona, blocker, target)
	}
	return fmt.Sprintf("[%s]\nBlocker: %s", persona, blocker)
}

// BuildRequestMessage formats a request message per SLACK_NOTES §6.1.
//
//	[Engineer-AE → CTO-AE]
//	Request: Review PR #42 for CI fix
func BuildRequestMessage(from, to, request string) string {
	return fmt.Sprintf("[%s → %s]\nRequest: %s", from, to, request)
}

// BuildDecisionMessage formats a decision message per SLACK_NOTES §6.1.
//
//	[CTO-AE]
//	Decision: Standardize on pytest
//	Reason: Better ecosystem support
func BuildDecisionMessage(persona, decision, reason string) string {
	return fmt.Sprintf("[%s]\nDecision: %s\nReason: %s", persona, decision, reason)
}
