package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// FallbackToolUseProvider wraps a basic ProviderClient and emulates tool-use
// via prompt injection + JSON response parsing. Used for providers whose API
// accepts the tools parameter but doesn't produce actual tool_calls output
// (e.g., greenthread/gpt-oss-120b).
type FallbackToolUseProvider struct {
	Client ProviderClient
}

func (f *FallbackToolUseProvider) CompleteWithTools(
	ctx context.Context,
	messages []ConversationMessage,
	tools []ToolDefinition,
	model string,
	maxTokens int,
) (ConversationResult, error) {
	// Convert ConversationMessages to plain Messages, injecting tool
	// descriptions into the system prompt and tool results into user messages.
	plainMsgs := convertToPlainMessages(messages, tools)

	slog.Debug("fallback_tool_use",
		"messages_in", len(messages),
		"tools", len(tools),
		"plain_messages", len(plainMsgs),
	)

	result, err := f.Client.Complete(ctx, plainMsgs, model, maxTokens)
	if err != nil {
		return ConversationResult{}, err
	}

	// Try to parse tool calls from the text response.
	slog.Info("fallback_llm_response", "text_len", len(result.Text), "text_preview", truncateStr(result.Text, 300))
	toolCalls, textContent := parseToolCallsFromText(result.Text)

	stopReason := StopReasonEndTurn
	if len(toolCalls) > 0 {
		stopReason = StopReasonToolUse
	}

	return ConversationResult{
		Message: ConversationMessage{
			Role:      "assistant",
			Content:   textContent,
			ToolCalls: toolCalls,
		},
		StopReason: stopReason,
		Usage:      result.Usage,
	}, nil
}

// convertToPlainMessages transforms a tool-use conversation into plain
// system/user/assistant messages with tool descriptions injected.
func convertToPlainMessages(messages []ConversationMessage, tools []ToolDefinition) []Message {
	var plain []Message
	toolPrompt := buildToolPrompt(tools)

	for _, m := range messages {
		switch {
		case m.Role == "system":
			// Append tool descriptions to the system prompt.
			content := m.Content
			if toolPrompt != "" {
				content += "\n\n" + toolPrompt
			}
			plain = append(plain, Message{Role: "system", Content: content})

		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			// Convert assistant tool calls into text representation.
			var parts []string
			if m.Content != "" {
				parts = append(parts, m.Content)
			}
			for _, tc := range m.ToolCalls {
				data, _ := json.Marshal(tc.Input)
				parts = append(parts, fmt.Sprintf(`{"tool_calls":[{"id":"%s","name":"%s","input":%s}]}`, tc.ID, tc.Name, string(data)))
			}
			plain = append(plain, Message{Role: "assistant", Content: strings.Join(parts, "\n")})

		case m.Role == "user" && len(m.ToolResults) > 0:
			// Convert tool results into a structured user message.
			var parts []string
			for _, tr := range m.ToolResults {
				label := "Tool Result"
				if tr.IsError {
					label = "Tool Error"
				}
				parts = append(parts, fmt.Sprintf("[%s for %s]:\n%s", label, tr.ToolUseID, tr.Content))
			}
			plain = append(plain, Message{Role: "user", Content: strings.Join(parts, "\n\n")})

		default:
			plain = append(plain, Message{Role: m.Role, Content: m.Content})
		}
	}

	return plain
}

// buildToolPrompt creates a text description of available tools for injection
// into the system prompt when native tool-use is unavailable.
func buildToolPrompt(tools []ToolDefinition) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Available Tools\n\n")
	b.WriteString("CRITICAL INSTRUCTIONS FOR TOOL USE:\n\n")
	b.WriteString("To call a tool, respond with ONLY this JSON and nothing else:\n")
	b.WriteString("{\"tool_calls\": [{\"name\": \"ToolName\", \"input\": {\"param\": \"value\"}}]}\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("1. NEVER explain what you plan to do. Just call the tool immediately.\n")
	b.WriteString("2. Your response must be ONLY the JSON. No text before or after.\n")
	b.WriteString("3. Call ONE tool per response. You will see the result, then can call another.\n")
	b.WriteString("4. Only respond with plain text when you are DONE and have no more tools to call.\n\n")

	for _, t := range tools {
		b.WriteString(fmt.Sprintf("### %s\n%s\n", t.Name, t.Description))
		if t.InputSchema != nil {
			schema, _ := json.MarshalIndent(t.InputSchema, "", "  ")
			b.WriteString(fmt.Sprintf("Parameters:\n```json\n%s\n```\n", string(schema)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// parseToolCallsFromText tries to extract tool calls from a plain text LLM
// response. Returns extracted tool calls and any remaining text content.
// Handles cases where the LLM includes prose before/after the JSON.
func parseToolCallsFromText(text string) ([]ToolCall, string) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences if present.
	cleaned := stripMarkdownFences(text)

	// Try multiple extraction strategies:
	// 1. Whole response is JSON
	// 2. JSON embedded in text (find first { and last })
	// 3. Bare JSON array

	candidates := []string{cleaned}

	// Also try extracting JSON from within prose text.
	if idx := strings.Index(cleaned, `{"tool_calls"`); idx >= 0 {
		// Find the matching closing brace.
		sub := cleaned[idx:]
		if end := findMatchingBrace(sub); end > 0 {
			candidates = append(candidates, sub[:end+1])
		}
	}

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		// Try {"tool_calls": [...]} format.
		var parsed struct {
			ToolCalls []struct {
				ID    string         `json:"id"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil && len(parsed.ToolCalls) > 0 {
			calls := make([]ToolCall, len(parsed.ToolCalls))
			for i, tc := range parsed.ToolCalls {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", i)
				}
				calls[i] = ToolCall{ID: id, Name: tc.Name, Input: tc.Input}
			}
			// Extract any prose text before the JSON as the text content.
			textBefore := ""
			if idx := strings.Index(cleaned, candidate); idx > 0 {
				textBefore = strings.TrimSpace(cleaned[:idx])
			}
			return calls, textBefore
		}

		// Try bare JSON array of tool calls.
		var bareArray []struct {
			Tool   string         `json:"tool"`
			Name   string         `json:"name"`
			Action string         `json:"action"`
			Input  map[string]any `json:"input"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(candidate), &bareArray); err == nil && len(bareArray) > 0 {
			calls := make([]ToolCall, 0, len(bareArray))
			for i, tc := range bareArray {
				name := tc.Name
				if name == "" {
					name = tc.Tool
				}
				if name == "" {
					continue
				}
				input := tc.Input
				if input == nil {
					input = tc.Params
				}
				calls = append(calls, ToolCall{
					ID:    fmt.Sprintf("call_%d", i),
					Name:  name,
					Input: input,
				})
			}
			if len(calls) > 0 {
				return calls, ""
			}
		}
	}

	// No tool calls found — return the text as-is.
	return nil, text
}

// findMatchingBrace finds the index of the closing } that matches the opening { at index 0.
func findMatchingBrace(s string) int {
	depth := 0
	inString := false
	escaped := false
	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stripMarkdownFences removes ```json ... ``` wrappers from text.
func stripMarkdownFences(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		// Remove first line (```json or ```)
		if len(lines) > 2 {
			lines = lines[1:]
		}
		// Remove last line if it's ```
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		return strings.Join(lines, "\n")
	}
	return text
}
