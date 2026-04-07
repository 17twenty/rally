package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAISDKClient uses the official OpenAI Go SDK for completions and tool-use.
// Works with any OpenAI-compatible endpoint (OpenAI, Greenthread/vLLM, etc.).
type OpenAISDKClient struct {
	client openai.Client
}

// NewOpenAISDKClient creates a client for the given base URL and API key.
func NewOpenAISDKClient(apiKey, baseURL string) *OpenAISDKClient {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL+"/v1"))
	}
	return &OpenAISDKClient{
		client: openai.NewClient(opts...),
	}
}

// Complete implements ProviderClient for basic text completion (legacy path).
func (c *OpenAISDKClient) Complete(ctx context.Context, messages []Message, model string, maxTokens int) (CompletionResult, error) {
	var msgs []openai.ChatCompletionMessageParamUnion
	for _, m := range messages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "user":
			msgs = append(msgs, openai.UserMessage(m.Content))
		case "assistant":
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		}
	}

	// Use streaming for all calls — vLLM's non-streaming endpoint is unreliable.
	stream := c.client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:     model,
		Messages:  msgs,
		MaxTokens: openai.Int(int64(maxTokens)),
	})

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		acc.AddChunk(stream.Current())
	}
	if err := stream.Err(); err != nil {
		return CompletionResult{}, fmt.Errorf("openai sdk: %w", err)
	}

	text := ""
	if len(acc.Choices) > 0 {
		text = acc.Choices[0].Message.Content
	}

	return CompletionResult{
		Text: text,
		Usage: Usage{
			InputTokens:  int(acc.Usage.PromptTokens),
			OutputTokens: int(acc.Usage.CompletionTokens),
		},
	}, nil
}

// CompleteWithTools implements ToolUseProvider for native tool-use.
// Uses stream=true to work around the vLLM bug where non-streaming
// requests return empty tool_calls.
func (c *OpenAISDKClient) CompleteWithTools(
	ctx context.Context,
	messages []ConversationMessage,
	tools []ToolDefinition,
	model string,
	maxTokens int,
) (ConversationResult, error) {
	// Build messages.
	var msgs []openai.ChatCompletionMessageParamUnion
	for _, m := range messages {
		switch {
		case m.Role == "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))

		case m.Role == "user" && len(m.ToolResults) > 0:
			for _, tr := range m.ToolResults {
				msgs = append(msgs, openai.ToolMessage(tr.Content, tr.ToolUseID))
			}

		case m.Role == "user":
			msgs = append(msgs, openai.UserMessage(m.Content))

		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			assistantMsg := openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				},
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Input)
				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID:   tc.ID,
					Type: "function",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(args),
					},
				})
			}
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})

		case m.Role == "assistant":
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		}
	}

	// Build tool definitions.
	var sdkTools []openai.ChatCompletionToolParam
	for _, t := range tools {
		schemaJSON, _ := json.Marshal(t.InputSchema)
		var schema openai.FunctionParameters
		_ = json.Unmarshal(schemaJSON, &schema)

		sdkTools = append(sdkTools, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  schema,
			},
		})
	}

	// Use streaming to work around vLLM tool_calls bug.
	stream := c.client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:     model,
		Messages:  msgs,
		Tools:     sdkTools,
		MaxTokens: openai.Int(int64(maxTokens)),
	})

	// Accumulate the streamed response.
	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
	}
	if err := stream.Err(); err != nil {
		return ConversationResult{}, fmt.Errorf("openai sdk stream: %w", err)
	}

	// Extract the accumulated result.
	choice := acc.Choices[0]
	msg := choice.Message

	// Build tool calls.
	var toolCalls []ToolCall
	for _, tc := range msg.ToolCalls {
		var input map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		toolCalls = append(toolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	// Determine stop reason.
	stopReason := StopReasonEndTurn
	switch choice.FinishReason {
	case "tool_calls":
		stopReason = StopReasonToolUse
	case "length":
		stopReason = StopReasonMaxTokens
	}

	return ConversationResult{
		Message: ConversationMessage{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: toolCalls,
		},
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  int(acc.Usage.PromptTokens),
			OutputTokens: int(acc.Usage.CompletionTokens),
		},
	}, nil
}
