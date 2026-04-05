package llm

import (
	"context"
	"encoding/json"
)

// ToolDefinition describes a tool the LLM can call.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolCall represents the LLM requesting a tool invocation.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult represents the outcome of executing a tool call.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// ConversationMessage is a message in a multi-turn tool-use conversation.
// Role is one of: "system", "user", "assistant".
// An assistant message may contain ToolCalls (when the LLM wants to use tools).
// A user message may contain ToolResults (results of executing tool calls).
type ConversationMessage struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

// StopReason indicates why the LLM stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"   // LLM finished; no more tool calls.
	StopReasonToolUse   StopReason = "tool_use"   // LLM wants to call one or more tools.
	StopReasonMaxTokens StopReason = "max_tokens"  // Hit max_tokens limit mid-generation.
)

// ConversationResult is the result of a tool-use-aware completion.
type ConversationResult struct {
	Message    ConversationMessage `json:"message"`
	StopReason StopReason          `json:"stop_reason"`
	Usage      Usage               `json:"usage"`
}

// ToolUseProvider extends ProviderClient with tool-use support.
// Providers that support native tool-use implement this interface.
// Those that don't fall back to prompt-based tool injection.
type ToolUseProvider interface {
	CompleteWithTools(
		ctx context.Context,
		messages []ConversationMessage,
		tools []ToolDefinition,
		model string,
		maxTokens int,
	) (ConversationResult, error)
}

// EstimateTokens gives a rough token estimate for a string (~4 chars per token).
func EstimateTokens(s string) int {
	return len(s) / 4
}

// EstimateMessageTokens estimates the total tokens in a conversation.
func EstimateMessageTokens(messages []ConversationMessage) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			data, _ := json.Marshal(tc.Input)
			total += EstimateTokens(tc.Name) + EstimateTokens(string(data))
		}
		for _, tr := range m.ToolResults {
			total += EstimateTokens(tr.Content)
		}
	}
	return total
}
