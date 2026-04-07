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
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/memory"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// ChatHandler handles chat UI and API routes.
// It delegates tool execution to AEAPIHandler — same code path as container AEs.
type ChatHandler struct {
	DB *db.DB
	AE *AEAPIHandler // shared service methods for tool execution + LLM
}

func (h *ChatHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

const rallySystemPrompt = `You are Rally, the operating system for AI-powered organizations.
You have tools to take action — use them. Don't suggest things the user should do manually; do it yourself.

When the user asks you to create a task, add a backlog item, delegate work, or send a message — use the appropriate tool and confirm it's done.

Be concise and action-oriented. Check real data before answering questions about the team or work.`

// Show handles GET /chat.
func (h *ChatHandler) Show(w http.ResponseWriter, r *http.Request) {
	data := pages.ChatData{}

	if h.DB != nil {
		ctx := r.Context()

		if companies, err := h.q().ListCompanies(ctx); err == nil {
			for _, c := range companies {
				data.Companies = append(data.Companies, domain.Company{
					ID:        c.ID,
					Name:      c.Name,
					Mission:   db.Deref(c.Mission),
					Status:    c.Status,
					CreatedAt: db.PgTime(c.CreatedAt),
				})
			}
		}

		if employees, err := h.q().ListAllEmployeesByCreatedAt(ctx); err == nil {
			for _, e := range employees {
				data.Employees = append(data.Employees, domain.Employee{
					ID:          e.ID,
					CompanyID:   e.CompanyID,
					Name:        db.Deref(e.Name),
					Role:        e.Role,
					Specialties: db.Deref(e.Specialties),
					Type:        e.Type,
					Status:      e.Status,
					SlackUserID: db.Deref(e.SlackUserID),
					CreatedAt:   db.PgTime(e.CreatedAt),
				})
			}
		}

		if len(data.Companies) > 0 {
			data.DefaultCompanyID = data.Companies[0].ID
		}
	}

	templ.Handler(pages.Chat(data)).ServeHTTP(w, r)
}

type chatMessageRequest struct {
	CompanyID string `json:"company_id"`
	AEID      string `json:"ae_id"`
	Message   string `json:"message"`
}

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
	modelRef := h.AE.LLMRouter.DefaultModel()

	// Ground the orchestrator prompt with real org state via service methods.
	if req.AEID == "" && h.DB != nil {
		var sb strings.Builder
		sb.WriteString(rallySystemPrompt)
		sb.WriteString("\n\n## Current organization state\n")

		companyID := req.CompanyID
		if companyID != "" {
			if team, err := h.AE.GetTeamMembers(ctx, companyID); err == nil {
				for _, m := range team {
					sb.WriteString(fmt.Sprintf("- %s (role: %s, type: %s, status: %s)\n", m.Name, m.Role, m.Type, m.Status))
				}
			} else {
				sb.WriteString("(no team data available)\n")
			}
		}

		sb.WriteString("\nIMPORTANT: Only reference data from your tools. Never invent or hallucinate data.\n")
		systemPrompt = sb.String()
	}

	// If ae_id provided, load employee + config and build system prompt.
	if req.AEID != "" && h.DB != nil {
		daoEmp, err := h.q().GetEmployee(ctx, req.AEID)
		if err == nil {
			emp := domain.Employee{
				ID: daoEmp.ID, CompanyID: daoEmp.CompanyID,
				Name: db.Deref(daoEmp.Name), Role: daoEmp.Role,
				Type: daoEmp.Type, Status: daoEmp.Status,
			}

			senderName = emp.Name
			if senderName == "" {
				senderName = emp.Role
			}

			ecRow, cfgErr := h.q().GetEmployeeConfig(ctx, emp.ID)
			var cfg domain.EmployeeConfig
			if cfgErr == nil && len(ecRow.Config) > 0 {
				_ = json.Unmarshal(ecRow.Config, &cfg)
			}

			if cfg.Cognition.DefaultModelRef != "" {
				modelRef = cfg.Cognition.DefaultModelRef
			}

			policyDoc, _ := h.q().GetCompanyPolicy(ctx, emp.CompanyID)
			systemPrompt = agent.BuildSystemPrompt(emp, cfg, cfg.Identity.SoulFile, policyDoc)
		}
	}

	// Build context from recent chat history.
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
				if strings.HasPrefix(recent[i].Content, "[CHAT]") {
					contextLines = append(contextLines, recent[i].Content)
				}
			}
		}
	}

	userPrompt := req.Message
	if len(contextLines) > 0 {
		userPrompt = "## Recent conversation context\n" + strings.Join(contextLines, "\n") + "\n\n## New message\n" + req.Message
	}

	// Agentic loop — same pattern as AE cycle, using service methods for tools.
	var response string
	tools := chatToolDefs()
	messages := []llm.ConversationMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	companyID := h.AE.ResolveCompanyID(ctx, employeeID)
	if companyID == "" {
		companyID = req.CompanyID
	}

	for turn := 0; turn < 10; turn++ {
		result, llmErr := h.AE.LLMRouter.CompleteWithTools(ctx, modelRef, messages, tools, 2000)
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
				tr := h.executeTool(ctx, tc, employeeID, companyID)
				toolResults = append(toolResults, tr)
			}
			messages = append(messages, llm.ConversationMessage{
				Role: "user", ToolResults: toolResults,
			})
			continue
		}

		response = result.Message.Content
		break
	}

	if response == "" {
		response = "(no response generated)"
	}

	// Save exchange to memory.
	if h.DB != nil {
		store := memory.NewMemoryStore(h.DB.Pool)
		content := fmt.Sprintf("[CHAT] Human: %s\n%s: %s", req.Message, senderName, response)
		_ = store.SaveEpisodic(ctx, employeeID, content, map[string]any{
			"source": "chat", "ae_id": req.AEID, "company_id": req.CompanyID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatMessageResponse{
		Response: response,
		Sender:   senderName,
		TS:       time.Now().Format("15:04:05"),
	})
}

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
		if rows.Scan(&rr.EmployeeID, &rr.Content, &rr.CreatedAt) == nil {
			rawRows = append(rawRows, rr)
		}
	}

	msgs := make([]chatHistoryMessage, 0, len(rawRows)*2)
	for i := len(rawRows) - 1; i >= 0; i-- {
		rr := rawRows[i]
		ts := rr.CreatedAt.Format("15:04:05")
		content := strings.TrimPrefix(rr.Content, "[CHAT] ")
		parts := strings.SplitN(content, "\n", 2)
		if len(parts) >= 1 {
			msgs = append(msgs, chatHistoryMessage{
				Sender: "user", SenderName: "You",
				Text: strings.TrimPrefix(parts[0], "Human: "), TS: ts,
			})
		}
		if len(parts) >= 2 {
			aePart := parts[1]
			colonIdx := strings.Index(aePart, ": ")
			name, text := "Rally", aePart
			if colonIdx >= 0 {
				name, text = aePart[:colonIdx], aePart[colonIdx+2:]
			}
			msgs = append(msgs, chatHistoryMessage{
				Sender: "ae", SenderName: name, Text: text, TS: ts,
			})
		}
	}

	_ = json.NewEncoder(w).Encode(msgs)
}

// chatToolDefs returns tool definitions for the /chat UI.
// These mirror the AE remote tools — same capabilities, no local tools.
func chatToolDefs() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "ListTeam",
			Description: "List all team members with their roles, types, and status.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "BacklogList",
			Description: "List work items for an AE. Optionally filter by status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "description": "Filter: todo, in_progress, blocked, done, or all"},
				},
			},
		},
		{
			Name:        "ListTasks",
			Description: "List all active tasks with assignees.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "ListProposedHires",
			Description: "List all proposed hires and their status.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "CreateTask",
			Description: "Create a task and assign it to an AE by role. The AE will see it in their next cycle.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":         map[string]any{"type": "string", "description": "Task title"},
					"description":   map[string]any{"type": "string", "description": "What needs to be done"},
					"assignee_role": map[string]any{"type": "string", "description": "Role to assign to (e.g. CEO, Go Developer)"},
				},
				"required": []string{"title", "assignee_role"},
			},
		},
		{
			Name:        "BacklogAdd",
			Description: "Add a work item to an AE's backlog.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Work item title"},
					"description": map[string]any{"type": "string", "description": "Details"},
					"priority":    map[string]any{"type": "string", "description": "critical, high, medium, low"},
				},
				"required": []string{"title"},
			},
		},
		{
			Name:        "SendMessageToAE",
			Description: "Send a direct message to an AE. They'll see it in their next cycle.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target_role": map[string]any{"type": "string", "description": "Role of the AE (e.g. CEO, Go Developer)"},
					"message":     map[string]any{"type": "string", "description": "The message"},
				},
				"required": []string{"target_role", "message"},
			},
		},
		{
			Name:        "SearchMemories",
			Description: "Search an AE's stored memories by keyword.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Keyword to search for"},
				},
				"required": []string{"query"},
			},
		},
	}
}

// executeTool dispatches a chat tool call to the shared AEAPIHandler service methods.
// Same code path as container AEs — no duplication.
func (h *ChatHandler) executeTool(ctx context.Context, tc llm.ToolCall, employeeID, companyID string) llm.ToolResult {
	result := func(data any) llm.ToolResult {
		b, _ := json.Marshal(data)
		return llm.ToolResult{ToolUseID: tc.ID, Content: string(b)}
	}
	errResult := func(err error) llm.ToolResult {
		return llm.ToolResult{ToolUseID: tc.ID, Content: fmt.Sprintf("Error: %s", err), IsError: true}
	}

	switch tc.Name {
	case "ListTeam":
		team, err := h.AE.GetTeamMembers(ctx, companyID)
		if err != nil {
			return errResult(err)
		}
		return result(map[string]any{"team": team, "count": len(team)})

	case "BacklogList":
		status, _ := tc.Input["status"].(string)
		items, err := h.AE.ListBacklog(ctx, employeeID, status)
		if err != nil {
			return errResult(err)
		}
		return result(map[string]any{"items": items, "count": len(items)})

	case "ListTasks":
		tasks, err := h.AE.ListAllActiveTasks(ctx)
		if err != nil {
			return errResult(err)
		}
		return result(map[string]any{"tasks": tasks, "count": len(tasks)})

	case "ListProposedHires":
		hires, err := h.AE.ListProposedHiresForCompany(ctx, companyID)
		if err != nil {
			return errResult(err)
		}
		return result(map[string]any{"hires": hires, "count": len(hires)})

	case "CreateTask":
		title, _ := tc.Input["title"].(string)
		desc, _ := tc.Input["description"].(string)
		role, _ := tc.Input["assignee_role"].(string)
		res, err := h.AE.CreateTaskForRole(ctx, companyID, title, desc, role)
		if err != nil {
			return errResult(err)
		}
		return result(res)

	case "BacklogAdd":
		title, _ := tc.Input["title"].(string)
		desc, _ := tc.Input["description"].(string)
		prio, _ := tc.Input["priority"].(string)
		res, err := h.AE.AddBacklogItem(ctx, employeeID, companyID, title, desc, prio)
		if err != nil {
			return errResult(err)
		}
		return result(res)

	case "SendMessageToAE":
		role, _ := tc.Input["target_role"].(string)
		msg, _ := tc.Input["message"].(string)
		res, err := h.AE.SendMessageToRole(ctx, employeeID, companyID, role, msg)
		if err != nil {
			return errResult(err)
		}
		return result(res)

	case "SearchMemories":
		query, _ := tc.Input["query"].(string)
		mems, err := h.AE.SearchMemoriesForEmployee(ctx, employeeID, query)
		if err != nil {
			return errResult(err)
		}
		return result(map[string]any{"memories": mems, "count": len(mems)})

	default:
		return llm.ToolResult{ToolUseID: tc.ID, Content: fmt.Sprintf("Unknown tool: %s", tc.Name), IsError: true}
	}
}
