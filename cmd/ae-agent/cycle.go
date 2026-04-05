package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// AgentCycle runs one observe → think → act → iterate cycle.
type AgentCycle struct {
	Rally          *RallyClient
	LocalTools     *LocalToolDispatcher
	SoulMD         string
	AEName         string
	AERole         string
	ModelRef       string
	MaxTurns       int              // max tool-use turns per cycle (default 25)
	RemoteToolDefs []RemoteToolDef  // tools available via Rally gateway
}

// Run executes a single heartbeat cycle using a multi-turn agentic loop.
// The agent observes context, then enters a loop where it calls the LLM,
// executes any requested tools, feeds results back, and repeats until the
// LLM is done or safety limits are hit.
func (c *AgentCycle) Run(ctx context.Context) error {
	maxTurns := c.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 25
	}

	// Wall-clock timeout for the entire cycle.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// 1. Observe — gather context from Rally
	slog.Info("cycle: observe")
	obs, err := c.Rally.FetchObservations(ctx)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}

	if len(obs.SlackEvents) == 0 && len(obs.Tasks) == 0 && len(obs.WorkItems) == 0 && len(obs.Messages) == 0 {
		slog.Info("cycle: nothing to do, sleeping")
		return nil
	}

	// 2. Build initial conversation — merge local + remote tool definitions
	tools := localToolDefs()
	for _, rt := range c.RemoteToolDefs {
		tools = append(tools, ToolDefinition{
			Name:        rt.Name,
			Description: rt.Description,
			InputSchema: rt.InputSchema,
		})
	}
	messages := []ChatMessage{
		{Role: "system", Content: c.buildSystemPrompt(obs)},
		{Role: "user", Content: c.buildObservationSummary(obs)},
	}

	// 3. Agentic loop — call LLM, execute tools, feed results back
	totalToolCalls := 0
	for turn := 0; turn < maxTurns; turn++ {
		slog.Info("cycle: turn", "turn", turn+1, "messages", len(messages))

		// Simple compaction: if conversation is getting large, trim old tool results.
		messages = microcompact(messages)

		result, err := c.Rally.ChatLLM(ctx, ChatLLMRequest{
			ModelRef:  c.ModelRef,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: 4096,
		})
		if err != nil {
			return fmt.Errorf("llm chat (turn %d): %w", turn+1, err)
		}

		// Append assistant message to conversation.
		messages = append(messages, result.Message)

		// Check stop condition.
		switch result.StopReason {
		case "end_turn":
			slog.Info("cycle: LLM finished", "turns", turn+1, "total_tool_calls", totalToolCalls)
			if result.Message.Content != "" {
				slog.Info("cycle: final response", "text", truncate(result.Message.Content, 200))
			}
			goto done

		case "max_tokens":
			slog.Warn("cycle: hit max_tokens", "turn", turn+1)
			goto done

		case "tool_use":
			if len(result.Message.ToolCalls) == 0 {
				slog.Warn("cycle: tool_use but no tool calls")
				goto done
			}

			// Execute each tool call and build result messages.
			var toolResults []ChatToolResult
			for _, tc := range result.Message.ToolCalls {
				slog.Info("cycle: tool_call", "tool", tc.Name, "id", tc.ID)
				tr := c.executeTool(ctx, tc)
				toolResults = append(toolResults, tr)
				totalToolCalls++

				// Log the tool execution to Rally.
				_ = c.Rally.SubmitLog(ctx, tc.Name, "", tc.Input,
					map[string]any{"content": truncate(tr.Content, 500)},
					!tr.IsError, "")
			}

			// Append tool results as a user message.
			messages = append(messages, ChatMessage{
				Role:        "user",
				ToolResults: toolResults,
			})

		default:
			slog.Warn("cycle: unexpected stop_reason", "stop_reason", result.StopReason)
			goto done
		}
	}

	slog.Warn("cycle: hit max turns", "max_turns", maxTurns, "total_tool_calls", totalToolCalls)

done:
	// 4. Store memory summary.
	summary := fmt.Sprintf("Cycle completed. Observed %d slack events, %d tasks. Executed %d tool calls across %d conversation turns.",
		len(obs.SlackEvents), len(obs.Tasks), totalToolCalls, len(messages))
	_ = c.Rally.StoreMemory(ctx, "episodic", summary, map[string]any{
		"tool_calls":   totalToolCalls,
		"message_count": len(messages),
	})

	return nil
}

func (c *AgentCycle) buildSystemPrompt(obs *Observations) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are %s, the %s.\n\n", c.AEName, c.AERole))

	if c.SoulMD != "" {
		sb.WriteString("## Your Identity\n")
		sb.WriteString(c.SoulMD)
		sb.WriteString("\n\n")
	}

	if obs.PolicyDoc != "" {
		sb.WriteString("## Company Policy\n")
		sb.WriteString(obs.PolicyDoc)
		sb.WriteString("\n\n")
	}

	sb.WriteString(`## How to Work
You have tools available to accomplish tasks. Use them as needed.

- Call one tool at a time. Review the result before deciding your next step.
- When reading files, use Read to see the content before making changes.
- Use Bash for shell commands. Use Read and Write for files.
- Use SlackSend to communicate with your team.
- When you are done with a task, respond with a text summary of what you accomplished.
- If you are unsure about something, gather more information first using your tools.
- Be concise and focused. Complete the task, then stop.
`)

	return sb.String()
}

func (c *AgentCycle) buildObservationSummary(obs *Observations) string {
	var sb strings.Builder
	sb.WriteString("## Current Observations\n\n")

	if len(obs.SlackEvents) > 0 {
		sb.WriteString("### Slack Messages\n")
		for _, evt := range obs.SlackEvents {
			text := ""
			if t, ok := evt.Payload["text"].(string); ok {
				text = t
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s in %s: %s\n", evt.EventType, evt.UserID, evt.Channel, text))
		}
		sb.WriteString("\n")
	}

	if len(obs.Tasks) > 0 {
		sb.WriteString("### Active Tasks\n")
		for _, t := range obs.Tasks {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", t.Status, t.Title, t.Description))
		}
		sb.WriteString("\n")
	}

	if len(obs.Memories) > 0 {
		sb.WriteString("### Recent Memories\n")
		for _, m := range obs.Memories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	if len(obs.WorkItems) > 0 {
		sb.WriteString("### Your Backlog\n")
		for _, wi := range obs.WorkItems {
			sb.WriteString(fmt.Sprintf("- [%s] (%s) %s (id: %s)\n", wi.Status, wi.Priority, wi.Title, wi.ID))
		}
		sb.WriteString("\n")
	}

	if len(obs.Messages) > 0 {
		sb.WriteString("### Messages from Colleagues\n")
		for _, m := range obs.Messages {
			sb.WriteString(fmt.Sprintf("- From %s: %s\n", m.FromID, m.Message))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nReview the observations above and take appropriate action using your tools.")
	return sb.String()
}

// executeTool dispatches a tool call to either the local executor or Rally.
func (c *AgentCycle) executeTool(ctx context.Context, tc ChatToolCall) ChatToolResult {
	var resultContent string
	var isError bool

	switch tc.Name {
	case "Bash":
		result, err := c.LocalTools.execShell(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "Read":
		result, err := c.LocalTools.execRead(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "Write":
		result, err := c.LocalTools.execWrite(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "Edit":
		result, err := c.LocalTools.execEdit(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "Grep":
		result, err := c.LocalTools.execGrep(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "Glob":
		result, err := c.LocalTools.execGlob(ctx, tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "ListFiles":
		result, err := c.LocalTools.execWorkspace(ctx, "list", tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	case "SlackSend":
		channel, _ := tc.Input["channel"].(string)
		text, _ := tc.Input["text"].(string)
		if channel == "" {
			channel = "#general"
		}
		err := c.Rally.SendSlack(ctx, channel, fmt.Sprintf("[%s] %s", c.AEName, text))
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = fmt.Sprintf(`{"status":"sent","channel":"%s"}`, channel)
		}

	// --- Work Tracking ---
	case "BacklogList":
		status, _ := tc.Input["status"].(string)
		data, err := c.Rally.BacklogList(ctx, status)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "BacklogAdd":
		title, _ := tc.Input["title"].(string)
		desc, _ := tc.Input["description"].(string)
		prio, _ := tc.Input["priority"].(string)
		parentID, _ := tc.Input["parent_id"].(string)
		sourceTaskID, _ := tc.Input["source_task_id"].(string)
		data, err := c.Rally.BacklogAdd(ctx, title, desc, prio, parentID, sourceTaskID)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "BacklogUpdate":
		id, _ := tc.Input["id"].(string)
		status, _ := tc.Input["status"].(string)
		note, _ := tc.Input["note"].(string)
		data, err := c.Rally.BacklogUpdate(ctx, id, status, note)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	// --- Collaboration ---
	case "Delegate":
		targetRole, _ := tc.Input["target_role"].(string)
		title, _ := tc.Input["title"].(string)
		desc, _ := tc.Input["description"].(string)
		taskCtx, _ := tc.Input["context"].(string)
		prio, _ := tc.Input["priority"].(string)
		data, err := c.Rally.DelegateWork(ctx, targetRole, title, desc, taskCtx, prio)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "Escalate":
		reason, _ := tc.Input["reason"].(string)
		taskCtx, _ := tc.Input["context"].(string)
		urgency, _ := tc.Input["urgency"].(string)
		data, err := c.Rally.EscalateToHuman(ctx, reason, taskCtx, urgency)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "SendMessage":
		targetRole, _ := tc.Input["target_role"].(string)
		message, _ := tc.Input["message"].(string)
		data, err := c.Rally.SendAEMessage(ctx, targetRole, message)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "UpdateTask":
		taskID, _ := tc.Input["task_id"].(string)
		status, _ := tc.Input["status"].(string)
		note, _ := tc.Input["note"].(string)
		data, err := c.Rally.UpdateTaskStatus(ctx, taskID, status, note)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "BrowserNavigate":
		result, err := c.LocalTools.execBrowser(ctx, "navigate", tc.Input)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			data, _ := json.Marshal(result)
			resultContent = string(data)
		}

	default:
		// Check if this is a remote tool (name matches a RemoteToolDef).
		if gwTool, gwAction, ok := c.resolveRemoteTool(tc.Name); ok {
			result, err := c.Rally.ExecuteRemoteTool(ctx, gwTool, gwAction, tc.Input)
			if err != nil {
				resultContent = fmt.Sprintf("Error: %s", err.Error())
				isError = true
			} else {
				data, _ := json.Marshal(result)
				resultContent = string(data)
			}
		} else {
			resultContent = fmt.Sprintf("Error: unknown tool %q", tc.Name)
			isError = true
		}
	}

	// Truncate very large results to avoid blowing up context.
	if len(resultContent) > 16000 {
		resultContent = resultContent[:16000] + "\n...[truncated, use offset/limit to read more]"
	}

	return ChatToolResult{
		ToolUseID: tc.ID,
		Content:   resultContent,
		IsError:   isError,
	}
}

// resolveRemoteTool looks up a tool name in the remote tool definitions
// and returns the gateway tool and action names. Returns false if not found.
func (c *AgentCycle) resolveRemoteTool(name string) (tool, action string, ok bool) {
	for _, rt := range c.RemoteToolDefs {
		if rt.Name == name {
			return rt.Tool, rt.Action, true
		}
	}
	return "", "", false
}

// microcompact performs lightweight context compaction.
// It replaces tool results older than the last keepLast with a short placeholder,
// reducing token usage without needing an LLM call.
func microcompact(messages []ChatMessage, keepLast ...int) []ChatMessage {
	keep := 3
	if len(keepLast) > 0 && keepLast[0] > 0 {
		keep = keepLast[0]
	}

	// Count tool result messages from the end.
	toolResultIndices := []int{}
	for i := len(messages) - 1; i >= 0; i-- {
		if len(messages[i].ToolResults) > 0 {
			toolResultIndices = append(toolResultIndices, i)
		}
	}

	if len(toolResultIndices) <= keep {
		return messages // nothing to compact
	}

	// Estimate total token size of tool results.
	totalTokens := 0
	for _, idx := range toolResultIndices {
		for _, tr := range messages[idx].ToolResults {
			totalTokens += len(tr.Content) / 4 // rough estimate
		}
	}

	// Only compact if over threshold.
	if totalTokens < 40000 {
		return messages
	}

	// Replace old tool results (beyond the last `keep`) with placeholders.
	oldIndices := toolResultIndices[keep:]
	compacted := make([]ChatMessage, len(messages))
	copy(compacted, messages)

	for _, idx := range oldIndices {
		newResults := make([]ChatToolResult, len(compacted[idx].ToolResults))
		for j, tr := range compacted[idx].ToolResults {
			newResults[j] = ChatToolResult{
				ToolUseID: tr.ToolUseID,
				Content:   "[previous tool result compacted]",
				IsError:   tr.IsError,
			}
		}
		compacted[idx] = ChatMessage{
			Role:        compacted[idx].Role,
			Content:     compacted[idx].Content,
			ToolResults: newResults,
		}
	}

	return compacted
}
