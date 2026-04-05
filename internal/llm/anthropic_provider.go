package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicSDKClient uses the official Anthropic Go SDK for completions and tool-use.
type AnthropicSDKClient struct {
	client anthropic.Client
}

// NewAnthropicSDKClient creates a client for the Anthropic Messages API.
func NewAnthropicSDKClient(apiKey string) *AnthropicSDKClient {
	return &AnthropicSDKClient{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
	}
}

// Complete implements ProviderClient for basic text completion.
func (c *AnthropicSDKClient) Complete(ctx context.Context, messages []Message, model string, maxTokens int) (CompletionResult, error) {
	var system string
	var msgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return CompletionResult{}, fmt.Errorf("anthropic sdk: %w", err)
	}

	text := ""
	for _, block := range resp.Content {
		if tb := block.AsText(); tb.Text != "" {
			text += tb.Text
		}
	}

	return CompletionResult{
		Text: text,
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}, nil
}

// CompleteWithTools implements ToolUseProvider for native tool-use.
func (c *AnthropicSDKClient) CompleteWithTools(
	ctx context.Context,
	messages []ConversationMessage,
	tools []ToolDefinition,
	model string,
	maxTokens int,
) (ConversationResult, error) {
	var system string
	var msgs []anthropic.MessageParam

	for _, m := range messages {
		switch {
		case m.Role == "system":
			system = m.Content

		case m.Role == "user" && len(m.ToolResults) > 0:
			var blocks []anthropic.ContentBlockParamUnion
			for _, tr := range m.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolUseID, tr.Content, tr.IsError))
			}
			msgs = append(msgs, anthropic.MessageParam{Role: "user", Content: blocks})

		case m.Role == "user":
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))

		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name))
			}
			msgs = append(msgs, anthropic.MessageParam{Role: "assistant", Content: blocks})

		case m.Role == "assistant":
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	// Build tool definitions.
	var sdkTools []anthropic.ToolUnionParam
	for _, t := range tools {
		props, _ := t.InputSchema["properties"]
		var required []string
		if req, ok := t.InputSchema["required"].([]string); ok {
			required = req
		} else if req, ok := t.InputSchema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
		}

		sdkTools = append(sdkTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: props,
					Required:   required,
				},
			},
		})
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
		Tools:     sdkTools,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return ConversationResult{}, fmt.Errorf("anthropic sdk: %w", err)
	}

	// Extract content and tool calls.
	var textContent string
	var toolCalls []ToolCall
	for _, block := range resp.Content {
		if tb := block.AsText(); tb.Text != "" {
			textContent += tb.Text
		}
		if tu := block.AsToolUse(); tu.ID != "" {
			var input map[string]any
			_ = json.Unmarshal(tu.Input, &input)
			toolCalls = append(toolCalls, ToolCall{
				ID:    tu.ID,
				Name:  tu.Name,
				Input: input,
			})
		}
	}

	stopReason := StopReasonEndTurn
	switch resp.StopReason {
	case "tool_use":
		stopReason = StopReasonToolUse
	case "max_tokens":
		stopReason = StopReasonMaxTokens
	}

	return ConversationResult{
		Message: ConversationMessage{
			Role:      "assistant",
			Content:   textContent,
			ToolCalls: toolCalls,
		},
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}, nil
}
