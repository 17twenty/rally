package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/agent"
	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/memory"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// ChatHandler handles chat UI and API routes.
type ChatHandler struct {
	DB        *db.DB
	LLMRouter *llm.Router
}

const rallySystemPrompt = "You are Rally, an intelligent operating system for organizations. You help users understand their organization, monitor AE agents, review logs, and coordinate work. Be concise and helpful."

// Show handles GET /chat.
func (h *ChatHandler) Show(w http.ResponseWriter, r *http.Request) {
	data := pages.ChatData{}

	if h.DB != nil {
		ctx := r.Context()

		// Load all companies
		compRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies ORDER BY created_at DESC`)
		if err == nil {
			defer compRows.Close()
			for compRows.Next() {
				var c domain.Company
				if scanErr := compRows.Scan(&c.ID, &c.Name, &c.Mission, &c.Status, &c.CreatedAt); scanErr == nil {
					data.Companies = append(data.Companies, c)
				}
			}
		}

		// Load all employees (AEs only — humans don't have LLM configs)
		empRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
			 FROM employees ORDER BY created_at ASC`)
		if err == nil {
			defer empRows.Close()
			for empRows.Next() {
				var e domain.Employee
				if scanErr := empRows.Scan(&e.ID, &e.CompanyID, &e.Name, &e.Role, &e.Specialties, &e.Type, &e.Status, &e.SlackUserID, &e.CreatedAt); scanErr == nil {
					data.Employees = append(data.Employees, e)
				}
			}
		}

		if len(data.Companies) > 0 {
			data.DefaultCompanyID = data.Companies[0].ID
		}
	}

	templ.Handler(pages.Chat(data)).ServeHTTP(w, r)
}

// chatMessageRequest is the JSON body for POST /chat/message.
type chatMessageRequest struct {
	CompanyID string `json:"company_id"`
	AEID      string `json:"ae_id"`
	Message   string `json:"message"`
}

// chatMessageResponse is the JSON response for POST /chat/message.
type chatMessageResponse struct {
	Response string `json:"response"`
	Sender   string `json:"sender"`
	TS       string `json:"ts"`
}

// Message handles POST /chat/message.
func (h *ChatHandler) Message(w http.ResponseWriter, r *http.Request) {
	var req chatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	senderName := "Rally"
	systemPrompt := rallySystemPrompt
	modelRef := h.LLMRouter.DefaultModel()

	// Ground the orchestrator prompt with real DB state.
	if req.AEID == "" && h.DB != nil {
		var sb strings.Builder
		sb.WriteString(rallySystemPrompt)
		sb.WriteString("\n\n## Current organization state\n")

		compRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, name, COALESCE(mission,''), status FROM companies ORDER BY created_at DESC`)
		if err == nil {
			var hasCompanies bool
			for compRows.Next() {
				var id, name, mission, status string
				if compRows.Scan(&id, &name, &mission, &status) == nil {
					if !hasCompanies {
						sb.WriteString("\n### Companies\n")
						hasCompanies = true
					}
					sb.WriteString(fmt.Sprintf("- %s (status: %s)", name, status))
					if mission != "" {
						sb.WriteString(fmt.Sprintf(" — %s", mission))
					}
					sb.WriteString("\n")

					// Employees for this company
					empRows, empErr := h.DB.Pool.Query(ctx,
						`SELECT COALESCE(name,''), role, type, status FROM employees WHERE company_id = $1 ORDER BY created_at ASC`, id)
					if empErr == nil {
						for empRows.Next() {
							var eName, eRole, eType, eStatus string
							if empRows.Scan(&eName, &eRole, &eType, &eStatus) == nil {
								label := eName
								if label == "" {
									label = eRole
								}
								sb.WriteString(fmt.Sprintf("  - %s (role: %s, type: %s, status: %s)\n", label, eRole, eType, eStatus))
							}
						}
						empRows.Close()
					}
				}
			}
			compRows.Close()
			if !hasCompanies {
				sb.WriteString("\nNo companies have been created yet. The database is empty.\n")
			}
		}

		sb.WriteString("\nIMPORTANT: Only reference data listed above. If no companies or agents exist, say so honestly. Never invent or hallucinate data.\n")
		systemPrompt = sb.String()
	}

	// If ae_id provided, load employee + config and build system prompt.
	if req.AEID != "" && h.DB != nil {
		var emp domain.Employee
		err := h.DB.Pool.QueryRow(ctx,
			`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
			 FROM employees WHERE id = $1`, req.AEID,
		).Scan(&emp.ID, &emp.CompanyID, &emp.Name, &emp.Role, &emp.Specialties, &emp.Type, &emp.Status, &emp.SlackUserID, &emp.CreatedAt)
		if err == nil {
			senderName = emp.Name
			if senderName == "" {
				senderName = emp.Role
			}

			// Load employee config for soul content
			var cfgRaw []byte
			cfgErr := h.DB.Pool.QueryRow(ctx,
				`SELECT config FROM employee_configs WHERE employee_id = $1 LIMIT 1`, emp.ID,
			).Scan(&cfgRaw)

			var cfg domain.EmployeeConfig
			if cfgErr == nil && len(cfgRaw) > 0 {
				_ = json.Unmarshal(cfgRaw, &cfg)
			}

			if cfg.Cognition.DefaultModelRef != "" {
				modelRef = cfg.Cognition.DefaultModelRef
			}

			var policyDoc string
			_ = h.DB.Pool.QueryRow(ctx,
				`SELECT COALESCE(policy_doc,'') FROM companies WHERE id = $1`, emp.CompanyID,
			).Scan(&policyDoc)

			systemPrompt = agent.BuildSystemPrompt(emp, cfg, cfg.Identity.SoulFile, policyDoc)
		}
	}

	// Build context from last 10 chat memory events for this entity.
	employeeID := req.AEID
	if employeeID == "" {
		employeeID = fmt.Sprintf("rally-%s", req.CompanyID)
	}

	var contextLines []string
	if h.DB != nil {
		store := memory.NewMemoryStore(h.DB.Pool)
		recent, err := store.GetByType(ctx, employeeID, "episodic", 10)
		if err == nil {
			for i := len(recent) - 1; i >= 0; i-- {
				m := recent[i]
				if strings.HasPrefix(m.Content, "[CHAT]") {
					contextLines = append(contextLines, m.Content)
				}
			}
		}
	}

	// Build user prompt with optional context.
	userPrompt := req.Message
	if len(contextLines) > 0 {
		userPrompt = "## Recent conversation context\n" + strings.Join(contextLines, "\n") + "\n\n## New message\n" + req.Message
	}

	// Call LLM router with tool-use support.
	// The chat handler runs a mini agentic loop (max 5 turns) so the AE
	// can check its real backlog, work items, etc. instead of hallucinating.
	var response string
	if h.LLMRouter != nil {
		tools := chatToolDefs()
		messages := []llm.ConversationMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}

		for turn := 0; turn < 5; turn++ {
			result, llmErr := h.LLMRouter.CompleteWithTools(ctx, modelRef, messages, tools, 1500)
			if llmErr != nil {
				response = fmt.Sprintf("(LLM unavailable: %v)", llmErr)
				break
			}

			messages = append(messages, result.Message)

			if result.StopReason == llm.StopReasonEndTurn || result.StopReason == llm.StopReasonMaxTokens {
				response = result.Message.Content
				break
			}

			if result.StopReason == llm.StopReasonToolUse && len(result.Message.ToolCalls) > 0 {
				var toolResults []llm.ToolResult
				for _, tc := range result.Message.ToolCalls {
					tr := h.executeChatTool(ctx, tc, employeeID)
					toolResults = append(toolResults, tr)
				}
				messages = append(messages, llm.ConversationMessage{
					Role:        "user",
					ToolResults: toolResults,
				})
				continue
			}

			response = result.Message.Content
			break
		}

		if response == "" {
			response = "(no response generated)"
		}
	} else {
		response = "(LLM router not configured)"
	}

	// Save exchange to memory_events.
	if h.DB != nil {
		store := memory.NewMemoryStore(h.DB.Pool)
		content := fmt.Sprintf("[CHAT] Human: %s\n%s: %s", req.Message, senderName, response)
		metadata := map[string]any{
			"source":     "chat",
			"ae_id":      req.AEID,
			"company_id": req.CompanyID,
		}
		_ = store.SaveEpisodic(ctx, employeeID, content, metadata)
	}

	ts := time.Now().Format("15:04:05")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatMessageResponse{
		Response: response,
		Sender:   senderName,
		TS:       ts,
	})
}

// chatHistoryMessage is a single message returned by the history endpoint.
type chatHistoryMessage struct {
	Sender     string `json:"sender"`
	SenderName string `json:"senderName"`
	Text       string `json:"text"`
	TS         string `json:"ts"`
}

// History handles GET /chat/history.
func (h *ChatHandler) History(w http.ResponseWriter, r *http.Request) {
	companyID := r.URL.Query().Get("company_id")
	w.Header().Set("Content-Type", "application/json")

	if h.DB == nil || companyID == "" {
		_ = json.NewEncoder(w).Encode([]chatHistoryMessage{})
		return
	}

	ctx := r.Context()

	// Fetch last 20 episodic memory events that start with [CHAT], for any
	// employee in this company (or the rally virtual employee for this company).
	rows, err := h.DB.Pool.Query(ctx, `
		SELECT me.employee_id, me.content, me.created_at
		FROM memory_events me
		WHERE me.content LIKE '[CHAT]%'
		  AND (
		    me.employee_id IN (SELECT id FROM employees WHERE company_id = $1)
		    OR me.employee_id = 'rally-' || $1
		  )
		ORDER BY me.created_at DESC
		LIMIT 20
	`, companyID)
	if err != nil {
		_ = json.NewEncoder(w).Encode([]chatHistoryMessage{})
		return
	}
	defer rows.Close()

	type rawRow struct {
		EmployeeID string
		Content    string
		CreatedAt  time.Time
	}
	var rawRows []rawRow
	for rows.Next() {
		var rr rawRow
		if scanErr := rows.Scan(&rr.EmployeeID, &rr.Content, &rr.CreatedAt); scanErr == nil {
			rawRows = append(rawRows, rr)
		}
	}

	// Reverse to chronological order.
	msgs := make([]chatHistoryMessage, 0, len(rawRows)*2)
	for i := len(rawRows) - 1; i >= 0; i-- {
		rr := rawRows[i]
		ts := rr.CreatedAt.Format("15:04:05")
		// Content format: "[CHAT] Human: {msg}\n{sender}: {response}"
		content := strings.TrimPrefix(rr.Content, "[CHAT] ")
		parts := strings.SplitN(content, "\n", 2)
		if len(parts) >= 1 {
			humanPart := strings.TrimPrefix(parts[0], "Human: ")
			msgs = append(msgs, chatHistoryMessage{
				Sender:     "user",
				SenderName: "You",
				Text:       humanPart,
				TS:         ts,
			})
		}
		if len(parts) >= 2 {
			// "SenderName: response text"
			aePart := parts[1]
			colonIdx := strings.Index(aePart, ": ")
			senderName := "Rally"
			text := aePart
			if colonIdx >= 0 {
				senderName = aePart[:colonIdx]
				text = aePart[colonIdx+2:]
			}
			msgs = append(msgs, chatHistoryMessage{
				Sender:     "ae",
				SenderName: senderName,
				Text:       text,
				TS:         ts,
			})
		}
	}

	_ = json.NewEncoder(w).Encode(msgs)
}

// chatToolDefs returns tool definitions available in the /chat web UI.
// These let AEs check their real work state instead of hallucinating.
func chatToolDefs() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "BacklogList",
			Description: "List your current work items. Returns real data from the database.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "description": "Filter: todo, in_progress, blocked, done, or all"},
				},
			},
		},
	}
}

// executeChatTool runs a tool call within the /chat handler (server-side, no container).
func (h *ChatHandler) executeChatTool(ctx context.Context, tc llm.ToolCall, employeeID string) llm.ToolResult {
	switch tc.Name {
	case "BacklogList":
		if h.DB == nil {
			return llm.ToolResult{ToolUseID: tc.ID, Content: "Database not available", IsError: true}
		}
		status, _ := tc.Input["status"].(string)
		query := `SELECT id, title, status, priority, updated_at FROM work_items WHERE owner_id = $1`
		args := []any{employeeID}
		if status != "" && status != "all" {
			query += ` AND status = $2`
			args = append(args, status)
		} else {
			query += ` AND status NOT IN ('done','cancelled')`
		}
		query += ` ORDER BY updated_at DESC LIMIT 20`

		rows, err := h.DB.Pool.Query(ctx, query, args...)
		if err != nil {
			return llm.ToolResult{ToolUseID: tc.ID, Content: fmt.Sprintf("Error: %s", err), IsError: true}
		}
		defer rows.Close()

		type item struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Status    string `json:"status"`
			Priority  string `json:"priority"`
			UpdatedAt string `json:"updated_at"`
		}
		var items []item
		for rows.Next() {
			var it item
			var updatedAt time.Time
			if rows.Scan(&it.ID, &it.Title, &it.Status, &it.Priority, &updatedAt) == nil {
				it.UpdatedAt = updatedAt.Format(time.RFC3339)
				items = append(items, it)
			}
		}

		if len(items) == 0 {
			return llm.ToolResult{ToolUseID: tc.ID, Content: `{"items":[],"count":0,"note":"No work items found. You have no active backlog."}`}
		}

		data, _ := json.Marshal(map[string]any{"items": items, "count": len(items)})
		return llm.ToolResult{ToolUseID: tc.ID, Content: string(data)}

	default:
		return llm.ToolResult{ToolUseID: tc.ID, Content: fmt.Sprintf("Unknown tool: %s", tc.Name), IsError: true}
	}
}
