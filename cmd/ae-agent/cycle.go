package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AgentCycle runs one heartbeat cycle using a context-driven agentic loop.
type AgentCycle struct {
	Rally          *RallyClient
	LocalTools     *LocalToolDispatcher
	SoulMD         string
	AEName         string
	AERole         string
	ModelRef       string
	MaxTurns       int              // max tool-use turns per cycle (default 25)
	RemoteToolDefs []RemoteToolDef  // tools available via Rally gateway
	CycleCount     int              // incremented each heartbeat
	ScratchPath    string           // /home/ae/scratch — for session state persistence
	LastSlackTS    string           // high-water mark: last Slack message_ts seen
}

// Run executes a single heartbeat cycle.
// The agent receives rich context about its identity, team, current state,
// and what's new — then the LLM decides what to do. No hardcoded logic.
func (c *AgentCycle) Run(ctx context.Context) error {
	maxTurns := c.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 25
	}
	c.CycleCount++

	// Use 8-min timeout for tool loop, reserve 2 min for reflection.
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	_ = parentCtx // used for reflection after tool loop

	// Refresh credentials each cycle (picks up tokens added after container start).
	setupCredentials(ctx, c.Rally, c.AEName)

	// Load persisted high-water mark if we don't have one yet (e.g. container restart).
	if c.LastSlackTS == "" {
		if ts, err := os.ReadFile(filepath.Join(c.ScratchPath, ".slack_hwm")); err == nil {
			c.LastSlackTS = strings.TrimSpace(string(ts))
		}
	}

	// 1. Gather all context.
	slog.Info("cycle: observe", "slack_since", c.LastSlackTS)
	obs, err := c.Rally.FetchObservations(ctx, c.LastSlackTS)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}

	// Advance the high-water mark to the latest new Slack event.
	for _, evt := range obs.SlackEvents {
		if evt.TS > c.LastSlackTS {
			c.LastSlackTS = evt.TS
		}
	}
	// Persist so it survives container restarts.
	if c.LastSlackTS != "" {
		_ = os.WriteFile(filepath.Join(c.ScratchPath, ".slack_hwm"), []byte(c.LastSlackTS), 0o644)
	}

	// Skip the LLM entirely if there's nothing to act on.
	// Filter out this AE's own Slack messages from the "new" count.
	myPrefix := "*" + c.AEName + ":*"
	newSlackCount := 0
	for _, evt := range obs.SlackEvents {
		if !strings.HasPrefix(evt.Text, myPrefix) {
			newSlackCount++
		}
	}
	hasWork := newSlackCount > 0 || len(obs.Tasks) > 0 ||
		len(obs.Messages) > 0
	// Also act if we have active work items (todo, in_progress, or blocked).
	for _, wi := range obs.WorkItems {
		if wi.Status == "todo" || wi.Status == "in_progress" || wi.Status == "blocked" {
			hasWork = true
			break
		}
	}
	if !hasWork {
		slog.Info("cycle: nothing new, skipping")
		return nil
	}

	// Use server-side model override if provided (allows dynamic model changes).
	modelRef := c.ModelRef
	if obs.ModelRef != "" {
		modelRef = obs.ModelRef
	}

	// Load session state from previous cycles (if any).
	sessionState := c.loadSessionState()

	// 2. Build the context document — the LLM decides what to do from this.
	tools := localToolDefs()
	for _, rt := range c.RemoteToolDefs {
		tools = append(tools, ToolDefinition{
			Name: rt.Name, Description: rt.Description, InputSchema: rt.InputSchema,
		})
	}

	// Use soul from observations (DB is the single source of truth).
	// Falls back to the env var soul if observations don't include it.
	soul := obs.SoulMD
	if soul == "" {
		soul = c.SoulMD
	}

	systemPrompt := c.buildContext(ctx, obs, soul, sessionState)
	userPrompt := c.buildSituation(obs)

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// 3. Agentic loop — LLM calls tools, sees results, iterates.
	totalToolCalls := 0
	var toolsUsed []string
	var finalText string
	for turn := 0; turn < maxTurns; turn++ {
		slog.Info("cycle: turn", "turn", turn+1, "messages", len(messages))

		messages = microcompact(messages)

		result, err := c.Rally.ChatLLM(ctx, ChatLLMRequest{
			ModelRef: modelRef, Messages: messages, Tools: tools, MaxTokens: 4096,
		})
		if err != nil {
			return fmt.Errorf("llm chat (turn %d): %w", turn+1, err)
		}

		messages = append(messages, result.Message)

		switch result.StopReason {
		case "end_turn":
			finalText = result.Message.Content
			slog.Info("cycle: done", "turns", turn+1, "tool_calls", totalToolCalls)
			if finalText != "" {
				slog.Info("cycle: summary", "text", truncate(finalText, 200))
			}
			goto done

		case "max_tokens":
			slog.Warn("cycle: hit max_tokens", "turn", turn+1)
			goto done

		case "tool_use":
			if len(result.Message.ToolCalls) == 0 {
				goto done
			}
			var toolResults []ChatToolResult
			for _, tc := range result.Message.ToolCalls {
				slog.Info("cycle: tool_call", "tool", tc.Name, "id", tc.ID)
				tr := c.executeTool(ctx, tc)
				toolResults = append(toolResults, tr)
				totalToolCalls++
				toolsUsed = append(toolsUsed, tc.Name)
				_ = c.Rally.SubmitLog(ctx, tc.Name, "", tc.Input,
					map[string]any{"content": truncate(tr.Content, 500)},
					!tr.IsError, "")
			}
			messages = append(messages, ChatMessage{Role: "user", ToolResults: toolResults})

		default:
			goto done
		}
	}

	slog.Warn("cycle: hit max turns", "max_turns", maxTurns)

done:
	// 4. Generate cycle reflection via LLM with a fresh timeout (not the tool loop timeout).
	reflCtx, reflCancel := context.WithTimeout(parentCtx, 2*time.Minute)
	defer reflCancel()

	if totalToolCalls > 0 || finalText != "" {
		// Build a condensed reflection request — the full conversation is too large
		// and causes the model to return empty. Send just a summary + prompt.
		var actionSummary strings.Builder
		actionSummary.WriteString(fmt.Sprintf("Cycle %d summary:\n", c.CycleCount))
		actionSummary.WriteString(fmt.Sprintf("Tools used: %s\n", strings.Join(unique(toolsUsed), ", ")))
		if finalText != "" {
			actionSummary.WriteString(fmt.Sprintf("Final response: %s\n", truncate(finalText, 500)))
		}
		// Include the last few tool calls for specificity.
		for i := len(messages) - 1; i >= 0 && i >= len(messages)-6; i-- {
			m := messages[i]
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					inputSnip, _ := json.Marshal(tc.Input)
					actionSummary.WriteString(fmt.Sprintf("- Called %s(%s)\n", tc.Name, truncate(string(inputSnip), 100)))
				}
			}
		}

		reflectionPrompt := `Summarise this cycle in 2-3 sentences for your future self. Include:
- What you did (actions taken, messages sent)
- Key decisions or things you said (so you stay consistent)
- What to follow up on next cycle
Be specific and factual. This is your memory — it helps you maintain continuity.`

		reflMessages := []ChatMessage{
			{Role: "system", Content: fmt.Sprintf("You are %s, the %s. Reflect on what you just did.", c.AEName, c.AERole)},
			{Role: "user", Content: actionSummary.String() + "\n" + reflectionPrompt},
		}
		reflResult, reflErr := c.Rally.ChatLLM(reflCtx, ChatLLMRequest{
			ModelRef: modelRef, Messages: reflMessages, Tools: nil, MaxTokens: 500,
		})
		if reflErr != nil {
			slog.Warn("cycle: reflection LLM failed, storing fallback", "err", reflErr)
			fallback := fmt.Sprintf("Cycle %d: Used %s. %s",
				c.CycleCount, strings.Join(unique(toolsUsed), ", "), truncate(finalText, 200))
			if storeErr := c.Rally.StoreMemory(reflCtx, "episodic", fallback, map[string]any{
				"cycle": c.CycleCount, "tool_calls": totalToolCalls,
			}); storeErr != nil {
				slog.Warn("cycle: StoreMemory failed", "err", storeErr)
			}
		} else if reflResult.Message.Content != "" {
			if storeErr := c.Rally.StoreMemory(reflCtx, "episodic", reflResult.Message.Content, map[string]any{
				"cycle": c.CycleCount, "tool_calls": totalToolCalls,
			}); storeErr != nil {
				slog.Warn("cycle: StoreMemory failed", "err", storeErr)
			} else {
				slog.Info("cycle: reflection stored", "content", truncate(reflResult.Message.Content, 200))
			}
		} else {
			slog.Warn("cycle: reflection LLM returned empty, storing fallback")
			fallback := fmt.Sprintf("Cycle %d: Used %s. %s",
				c.CycleCount, strings.Join(unique(toolsUsed), ", "), truncate(finalText, 200))
			if storeErr := c.Rally.StoreMemory(reflCtx, "episodic", fallback, map[string]any{
				"cycle": c.CycleCount, "tool_calls": totalToolCalls,
			}); storeErr != nil {
				slog.Warn("cycle: StoreMemory fallback failed", "err", storeErr)
			}
		}
	} else {
		_ = c.Rally.StoreMemory(reflCtx, "episodic",
			fmt.Sprintf("Cycle %d: No actions taken.", c.CycleCount),
			map[string]any{"cycle": c.CycleCount})
	}

	return nil
}

// buildContext assembles the system prompt from the agent's full context.
// This is the "who am I, where do I work, what am I doing" document.
// The LLM makes all decisions from this context — no hardcoded logic.
func (c *AgentCycle) buildContext(ctx context.Context, obs *Observations, soulMD, sessionState string) string {
	var sb strings.Builder

	// Identity.
	sb.WriteString(fmt.Sprintf("You are %s, the %s at %s.\n\n", c.AEName, c.AERole, obs.Company.Name))

	if soulMD != "" {
		sb.WriteString(soulMD)
		sb.WriteString("\n\n")
	}

	// Environment.
	sb.WriteString("## Your Environment\n")
	sb.WriteString(fmt.Sprintf("- Company: %s — %s\n", obs.Company.Name, obs.Company.Mission))
	sb.WriteString(fmt.Sprintf("- Date: %s\n", time.Now().Format("2006-01-02 15:04")))
	sb.WriteString(fmt.Sprintf("- Heartbeat: cycle #%d\n", c.CycleCount))
	sb.WriteString("- Workspace: /workspace (shared with all team members — clone repos here)\n")
	sb.WriteString("- Scratch: /home/ae/scratch (your private working space)\n")

	// Show available credentials so the AE knows what integrations it has.
	if creds, err := c.Rally.ListCredentials(ctx); err == nil {
		var credResult struct {
			Credentials []struct {
				Provider string `json:"provider"`
				Status   string `json:"status"`
			} `json:"credentials"`
		}
		if json.Unmarshal(creds, &credResult) == nil && len(credResult.Credentials) > 0 {
			sb.WriteString("- Credentials: ")
			for i, cr := range credResult.Credentials {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("%s (%s)", cr.Provider, cr.Status))
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")

	// Company policy.
	if obs.PolicyDoc != "" {
		sb.WriteString("## Company Policy\n")
		sb.WriteString(obs.PolicyDoc)
		sb.WriteString("\n\n")
	}

	// Team roster.
	if len(obs.Team) > 0 {
		sb.WriteString("## Your Team\n")
		for _, m := range obs.Team {
			label := m.Name
			if label == "" {
				label = m.Role
			}
			sb.WriteString(fmt.Sprintf("- %s (%s, %s)\n", label, m.Role, m.Type))
		}
		sb.WriteString("\n")
	}

	// Flag new team members (AEs created in the last 10 minutes).
	// This helps existing AEs welcome newcomers.
	for _, m := range obs.Team {
		if m.Type == "ae" && m.Name != c.AEName+" ("+c.AERole+")" {
			// Include all AEs in the team section — the LLM will notice new ones
			// compared to its session state / memories.
		}
	}

	// Pending hire proposals.
	if len(obs.ProposedHires) > 0 {
		sb.WriteString("## Pending Hire Proposals (awaiting human approval)\n")
		for _, ph := range obs.ProposedHires {
			sb.WriteString(fmt.Sprintf("- %s [%s]\n", ph.Role, ph.Status))
		}
		sb.WriteString("\n")
	}

	// Session state from previous cycles.
	if sessionState != "" {
		sb.WriteString("## Your Session Notes (from previous cycles)\n")
		sb.WriteString(sessionState)
		sb.WriteString("\n\n")
	}

	// How to work — applicable to any role.
	sb.WriteString(`## How to Work
You are an AI employee. You take action — you don't schedule meetings or pretend to be human.

### Slack
- Send ONE message per topic. Post the result, not each step.
- Do NOT post status updates ("all systems nominal") to Slack. Ever.
- If nothing to do, end silently. Do NOT post to Slack.
- When tools like ProposeHire handle notifications, don't duplicate them.

### Your Backlog (Work Items)
- BacklogList: see your current work items
- BacklogAdd: break down work into trackable items
- BacklogUpdate: mark items in_progress, done, blocked, or cancelled
- Check BacklogList BEFORE creating items — don't create duplicates.

### Assigned Tasks (from others)
- Tasks appear in your observations when someone delegates to you.
- UpdateTask: mark them in_progress or done when complete.
- The system auto-completes linked work items when you mark a task done.

### Memory
- Use Remember to store decisions, context, and things you've said.
- Your reflections are automatically stored at cycle end.

### Collaboration
- Delegate: assign work to a colleague by role
- SendMessage: quick question to a colleague
- Escalate: flag something for human attention
- ProposeHire: CEO only — propose new team members

### Important
- If you need credentials or access you don't have, add a BacklogItem as blocked with a note about what you need. Escalate ONCE, then move on to other work.
- RecallMemory: search your stored memories for past decisions and context.
`)

	return sb.String()
}

// buildSituation assembles the user message — what's happening RIGHT NOW.
// This is the "what changed, what needs attention" snapshot.
func (c *AgentCycle) buildSituation(obs *Observations) string {
	var sb strings.Builder
	sb.WriteString("## What's Happening Now\n\n")

	// Current work state.
	inProgress := 0
	todo := 0
	blocked := 0
	for _, wi := range obs.WorkItems {
		switch wi.Status {
		case "in_progress":
			inProgress++
		case "todo":
			todo++
		case "blocked":
			blocked++
		}
	}

	if inProgress > 0 || todo > 0 || blocked > 0 {
		sb.WriteString("### Your Work Items\n")
		for _, wi := range obs.WorkItems {
			sb.WriteString(fmt.Sprintf("- [%s] (%s) %s (id: %s)\n", wi.Status, wi.Priority, wi.Title, wi.ID))
		}
		sb.WriteString("\n")
	}

	// New tasks assigned.
	if len(obs.Tasks) > 0 {
		sb.WriteString("### Tasks Assigned to You\n")
		for _, t := range obs.Tasks {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s (id: %s)\n", t.Status, t.Title, t.Description, t.ID))
		}
		sb.WriteString("\n")
	}

	// Slack — split into context (already seen) and new (act on these).
	// Filter out this AE's own messages (prefixed with *Name:*).
	myPrefix := "*" + c.AEName + ":*"

	var contextMsgs, newMsgs []SlackEventObs
	for _, evt := range obs.SlackContext {
		if !strings.HasPrefix(evt.Text, myPrefix) {
			contextMsgs = append(contextMsgs, evt)
		}
	}
	for _, evt := range obs.SlackEvents {
		if !strings.HasPrefix(evt.Text, myPrefix) {
			newMsgs = append(newMsgs, evt)
		}
	}

	if len(contextMsgs) > 0 {
		sb.WriteString("### Recent Slack (already seen — for context only, do NOT reply to these)\n")
		for _, evt := range contextMsgs {
			sb.WriteString(fmt.Sprintf("- %s in %s: \"%s\"\n", evt.UserID, evt.Channel, evt.Text))
		}
		sb.WriteString("\n")
	}
	if len(newMsgs) > 0 {
		sb.WriteString("### NEW Slack Messages (respond to these)\n")
		for _, evt := range newMsgs {
			sb.WriteString(fmt.Sprintf("- %s in %s: \"%s\"\n", evt.UserID, evt.Channel, evt.Text))
		}
		lastChannel := newMsgs[len(newMsgs)-1].Channel
		sb.WriteString(fmt.Sprintf("Reply using SlackSend with channel=\"%s\" to respond in the same conversation.\n\n", lastChannel))
	}

	// Messages from colleagues.
	if len(obs.Messages) > 0 {
		sb.WriteString("### Messages from Colleagues\n")
		for _, m := range obs.Messages {
			sb.WriteString(fmt.Sprintf("- From %s: %s\n", m.FromID, m.Message))
		}
		sb.WriteString("\n")
	}

	// Recent memories.
	if len(obs.Memories) > 0 {
		sb.WriteString("### Your Recent Activity\n")
		for _, m := range obs.Memories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	// If truly nothing is happening.
	if len(obs.Tasks) == 0 && len(obs.SlackEvents) == 0 && len(obs.Messages) == 0 &&
		len(obs.WorkItems) == 0 {
		sb.WriteString("Nothing new to act on. End this cycle silently.\n")
	}

	sb.WriteString(`
### Before you act
1. Look at your team roster, your backlog, and what's new.
2. Has anything changed that resolves an open item? (e.g. a hire completed, a task was done by someone else) If so, mark those items done first.
3. Then focus on what's genuinely new — new Slack messages, new tasks, unfinished work.
4. If nothing is new and no items need attention, end the cycle silently. Do NOT post to Slack.
`)
	return sb.String()
}

// loadSessionState reads the persistent session state from the AE's scratch directory.
func (c *AgentCycle) loadSessionState() string {
	path := filepath.Join(c.ScratchPath, "session_state.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // no session state yet
	}
	content := string(data)
	// Cap at ~2000 chars to avoid bloating the prompt.
	if len(content) > 2000 {
		content = content[:2000] + "\n...[truncated]"
	}
	return content
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
		// Strip any name prefix the model included — Rally adds the persona prefix.
		// Handles: "Alex: ", "*Alex:* ", "Alex (CEO): ", "*Alex (CEO):* ", etc.
		for _, prefix := range []string{
			"*" + c.AEName + " (" + c.AERole + "):* ",
			c.AEName + " (" + c.AERole + "): ",
			"*" + c.AEName + ":* ",
			c.AEName + ": ",
		} {
			if strings.HasPrefix(text, prefix) {
				text = strings.TrimPrefix(text, prefix)
				break
			}
		}
		err := c.Rally.SendSlack(ctx, channel, fmt.Sprintf("*%s:* %s", c.AEName, text))
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

	// --- Memory ---
	case "Remember":
		content, _ := tc.Input["content"].(string)
		memType, _ := tc.Input["type"].(string)
		if memType == "" {
			memType = "reflection"
		}
		err := c.Rally.StoreMemory(ctx, memType, content, map[string]any{"source": "explicit"})
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = fmt.Sprintf(`{"status":"remembered","type":"%s"}`, memType)
		}

	case "RecallMemory":
		query, _ := tc.Input["query"].(string)
		data, err := c.Rally.SearchMemories(ctx, query)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	// --- Credentials ---
	case "ListCredentials":
		data, err := c.Rally.ListCredentials(ctx)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	// --- Hiring ---
	case "ProposeHire":
		role, _ := tc.Input["role"].(string)
		dept, _ := tc.Input["department"].(string)
		rationale, _ := tc.Input["rationale"].(string)
		reportsTo, _ := tc.Input["reports_to"].(string)
		channel, _ := tc.Input["channel"].(string)
		data, err := c.Rally.ProposeHire(ctx, role, dept, rationale, reportsTo, channel)
		if err != nil {
			resultContent = fmt.Sprintf("Error: %s", err.Error())
			isError = true
		} else {
			resultContent = string(data)
		}

	case "ListTeam":
		data, err := c.Rally.ListTeam(ctx)
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
		// Check remote tools.
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

	if len(resultContent) > 16000 {
		resultContent = resultContent[:16000] + "\n...[truncated, use offset/limit to read more]"
	}

	return ChatToolResult{
		ToolUseID: tc.ID,
		Content:   resultContent,
		IsError:   isError,
	}
}

// resolveRemoteTool looks up a tool name in the remote tool definitions.
func (c *AgentCycle) resolveRemoteTool(name string) (tool, action string, ok bool) {
	for _, rt := range c.RemoteToolDefs {
		if rt.Name == name {
			return rt.Tool, rt.Action, true
		}
	}
	return "", "", false
}

// microcompact performs lightweight context compaction.
func microcompact(messages []ChatMessage, keepLast ...int) []ChatMessage {
	keep := 3
	if len(keepLast) > 0 && keepLast[0] > 0 {
		keep = keepLast[0]
	}

	toolResultIndices := []int{}
	for i := len(messages) - 1; i >= 0; i-- {
		if len(messages[i].ToolResults) > 0 {
			toolResultIndices = append(toolResultIndices, i)
		}
	}

	if len(toolResultIndices) <= keep {
		return messages
	}

	totalTokens := 0
	for _, idx := range toolResultIndices {
		for _, tr := range messages[idx].ToolResults {
			totalTokens += len(tr.Content) / 4
		}
	}

	if totalTokens < 40000 {
		return messages
	}

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

// unique returns a deduplicated slice preserving order.
func unique(s []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
