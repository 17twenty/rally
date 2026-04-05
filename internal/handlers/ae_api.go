package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/memory"
	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/tools"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/internal/workspace"
)

// AEAPIHandler handles the /api/ae/* endpoints used by AE agent containers.
type AEAPIHandler struct {
	DB             *db.DB
	LLMRouter      *llm.Router
	Vault          *vault.CredentialVault
	SlackClient    *slack.SlackClient
	WorkspaceStore *workspace.WorkspaceStore
}

// AEAuthMiddleware validates the bearer token on /api/ae/* routes and
// injects employee_id and company_id into the request context.
func AEAuthMiddleware(db *db.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")

		employeeID, companyID, err := container.ValidateToken(r.Context(), db.Pool, token)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		// Store in headers for downstream handlers (simpler than context values)
		r.Header.Set("X-AE-Employee-ID", employeeID)
		r.Header.Set("X-AE-Company-ID", companyID)
		next.ServeHTTP(w, r)
	})
}

// Register handles POST /api/ae/register — AE announces it's alive.
func (h *AEAPIHandler) Register(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	slog.Info("ae_register", "employee_id", employeeID)

	_, err := h.DB.Pool.Exec(r.Context(),
		`UPDATE employees SET container_status = 'running' WHERE id = $1`, employeeID)
	if err != nil {
		slog.Warn("ae_register: update failed", "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

// Heartbeat handles POST /api/ae/heartbeat — AE reports it's alive.
func (h *AEAPIHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")

	var body struct {
		Cycle int `json:"cycle"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	slog.Info("ae_heartbeat", "employee_id", employeeID, "cycle", body.Cycle)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Observations handles GET /api/ae/observations — returns context for the AE's cycle.
func (h *AEAPIHandler) Observations(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	type slackEventObs struct {
		ID        string         `json:"id"`
		EventType string         `json:"event_type"`
		Channel   string         `json:"channel"`
		UserID    string         `json:"user_id"`
		ThreadTS  string         `json:"thread_ts"`
		Payload   map[string]any `json:"payload"`
	}

	type memoryObs struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	type taskObs struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}

	// Fetch unprocessed Slack events for this company
	var slackEvents []slackEventObs
	rows, err := h.DB.Pool.Query(ctx, `
		SELECT id, event_type, COALESCE(channel,''), COALESCE(user_id,''), COALESCE(thread_ts,''), payload
		FROM slack_events
		WHERE company_id = $1 AND processed_at IS NULL
		ORDER BY created_at ASC LIMIT 10
	`, companyID)
	if err == nil {
		for rows.Next() {
			var evt slackEventObs
			var payloadBytes []byte
			if rows.Scan(&evt.ID, &evt.EventType, &evt.Channel, &evt.UserID, &evt.ThreadTS, &payloadBytes) == nil {
				_ = json.Unmarshal(payloadBytes, &evt.Payload)
				slackEvents = append(slackEvents, evt)
			}
		}
		rows.Close()
	}

	// Fetch recent memories
	var memories []memoryObs
	store := memory.NewMemoryStore(h.DB.Pool)
	recent, err := store.GetByType(ctx, employeeID, "episodic", 5)
	if err == nil {
		for _, m := range recent {
			memories = append(memories, memoryObs{Type: m.Type, Content: m.Content})
		}
	}

	// Fetch active tasks assigned to this AE
	var tasks []taskObs
	taskRows, err := h.DB.Pool.Query(ctx, `
		SELECT id, title, COALESCE(description,''), status
		FROM tasks WHERE assignee_id = $1 AND status NOT IN ('done','cancelled')
		ORDER BY created_at DESC LIMIT 10
	`, employeeID)
	if err == nil {
		for taskRows.Next() {
			var t taskObs
			if taskRows.Scan(&t.ID, &t.Title, &t.Description, &t.Status) == nil {
				tasks = append(tasks, t)
			}
		}
		taskRows.Close()
	}

	// Fetch company policy
	var policyDoc string
	_ = h.DB.Pool.QueryRow(ctx,
		`SELECT COALESCE(policy_doc,'') FROM companies WHERE id = $1`, companyID,
	).Scan(&policyDoc)

	// Fetch active work items (backlog) for this AE.
	type workItemObs struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	}
	var workItems []workItemObs
	wiRows, err := h.DB.Pool.Query(ctx,
		`SELECT id, title, status, priority FROM work_items
		 WHERE owner_id = $1 AND status NOT IN ('done','cancelled')
		 ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at ASC
		 LIMIT 20`, employeeID)
	if err == nil {
		for wiRows.Next() {
			var wi workItemObs
			if wiRows.Scan(&wi.ID, &wi.Title, &wi.Status, &wi.Priority) == nil {
				workItems = append(workItems, wi)
			}
		}
		wiRows.Close()
	}

	// Fetch unread messages from other AEs.
	type msgObs struct {
		FromID  string `json:"from_id"`
		Message string `json:"message"`
	}
	var messages []msgObs
	msgRows, err := h.DB.Pool.Query(ctx,
		`SELECT from_id, message FROM ae_messages WHERE to_id = $1 AND read = false ORDER BY created_at ASC LIMIT 10`, employeeID)
	if err == nil {
		for msgRows.Next() {
			var m msgObs
			if msgRows.Scan(&m.FromID, &m.Message) == nil {
				messages = append(messages, m)
			}
		}
		msgRows.Close()
		// Mark as read.
		if len(messages) > 0 {
			_, _ = h.DB.Pool.Exec(ctx, `UPDATE ae_messages SET read = true WHERE to_id = $1 AND read = false`, employeeID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"slack_events": slackEvents,
		"memories":     memories,
		"tasks":        tasks,
		"work_items":   workItems,
		"messages":     messages,
		"policy_doc":   policyDoc,
	})
}

// LLMComplete handles POST /api/ae/llm/complete — proxies LLM calls.
func (h *AEAPIHandler) LLMComplete(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")

	var req struct {
		ModelRef     string `json:"model_ref"`
		SystemPrompt string `json:"system_prompt"`
		UserPrompt   string `json:"user_prompt"`
		MaxTokens    int    `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.MaxTokens <= 0 {
		req.MaxTokens = 1000
	}

	slog.Info("ae_llm_request",
		"employee_id", employeeID,
		"model_ref", req.ModelRef,
		"system_len", len(req.SystemPrompt),
		"user_len", len(req.UserPrompt),
		"max_tokens", req.MaxTokens,
	)

	response, err := h.LLMRouter.Complete(r.Context(), req.ModelRef, req.SystemPrompt, req.UserPrompt, req.MaxTokens)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"response": response})
}

// LLMChat handles POST /api/ae/llm/chat — multi-turn tool-use completion.
// This is the new endpoint for the agentic loop. The AE sends the full
// conversation (including tool results) and gets back the next assistant
// message with optional tool calls.
func (h *AEAPIHandler) LLMChat(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")

	var req struct {
		ModelRef  string                     `json:"model_ref"`
		Messages  []llm.ConversationMessage  `json:"messages"`
		Tools     []llm.ToolDefinition       `json:"tools"`
		MaxTokens int                        `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.MaxTokens <= 0 {
		req.MaxTokens = 4096
	}
	if req.ModelRef == "" {
		req.ModelRef = h.LLMRouter.DefaultModel()
	}

	slog.Info("ae_llm_chat",
		"employee_id", employeeID,
		"model_ref", req.ModelRef,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"max_tokens", req.MaxTokens,
	)

	result, err := h.LLMRouter.CompleteWithTools(r.Context(), req.ModelRef, req.Messages, req.Tools, req.MaxTokens)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// SlackSend handles POST /api/ae/slack/send — sends a Slack message via Rally.
func (h *AEAPIHandler) SlackSend(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")

	var req struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	slog.Info("ae_slack_send", "employee_id", employeeID, "channel", req.Channel, "text_len", len(req.Text))

	if h.SlackClient == nil {
		http.Error(w, `{"error":"slack not configured"}`, http.StatusServiceUnavailable)
		return
	}

	ts, err := h.SlackClient.PostMessage(r.Context(), req.Channel, req.Text)
	if err != nil {
		slog.Warn("ae_slack_send failed", "employee_id", employeeID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "sent", "ts": ts})
}

// StoreMemory handles POST /api/ae/memory — stores a memory event.
func (h *AEAPIHandler) StoreMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmployeeID string         `json:"employee_id"`
		Type       string         `json:"type"`
		Content    string         `json:"content"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	store := memory.NewMemoryStore(h.DB.Pool)
	if err := store.SaveEpisodic(r.Context(), req.EmployeeID, req.Content, req.Metadata); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "stored"})
}

// SubmitLog handles POST /api/ae/logs — records a tool execution log.
func (h *AEAPIHandler) SubmitLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmployeeID string         `json:"employee_id"`
		Tool       string         `json:"tool"`
		Action     string         `json:"action"`
		Input      map[string]any `json:"input"`
		Output     map[string]any `json:"output"`
		Success    bool           `json:"success"`
		TraceID    string         `json:"trace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	companyID := r.Header.Get("X-AE-Company-ID")
	inputJSON, _ := json.Marshal(req.Input)
	outputJSON, _ := json.Marshal(req.Output)

	_, err := h.DB.Pool.Exec(r.Context(),
		`INSERT INTO tool_logs (id, employee_id, company_id, tool, action, input, output, success, trace_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		newID(), req.EmployeeID, companyID, req.Tool, req.Action, inputJSON, outputJSON, req.Success, req.TraceID,
	)
	if err != nil {
		slog.Warn("ae_submit_log: insert failed", "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "logged"})
}

// FetchCredential handles GET /api/ae/credentials/{provider} — returns a decrypted credential.
func (h *AEAPIHandler) FetchCredential(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	employeeID := r.Header.Get("X-AE-Employee-ID")

	if h.Vault == nil {
		http.Error(w, `{"error":"vault not configured"}`, http.StatusServiceUnavailable)
		return
	}

	token, err := h.Vault.Get(r.Context(), employeeID, provider)
	if err != nil {
		http.Error(w, `{"error":"credential not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}

// ExecuteTool handles POST /api/ae/tools/execute — runs a tool via the ToolGateway.
// This gives AE agents access to all gateway tools (GitHub, Google Workspace,
// Figma, etc.) without needing per-tool endpoints.
func (h *AEAPIHandler) ExecuteTool(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	var req struct {
		Tool   string         `json:"tool"`
		Action string         `json:"action"`
		Input  map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Tool == "" || req.Action == "" {
		http.Error(w, `{"error":"tool and action are required"}`, http.StatusBadRequest)
		return
	}

	slog.Info("ae_tool_execute",
		"employee_id", employeeID,
		"tool", req.Tool,
		"action", req.Action,
	)

	// Load employee config to get ToolsConfig.
	var toolsConfig map[string]bool
	var configJSON []byte
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT config FROM employee_configs WHERE employee_id = $1 LIMIT 1`,
		employeeID,
	).Scan(&configJSON)
	if err == nil {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(configJSON, &cfg); jsonErr == nil {
			toolsConfig = cfg.Tools
		}
	}

	// Build a ToolGateway for this request.
	gw := &tools.ToolGateway{
		DB:             h.DB.Pool,
		SlackClient:    h.SlackClient,
		WorkspaceStore: h.WorkspaceStore,
		Vault:          h.Vault,
		EmployeeID:     employeeID,
		CompanyID:      companyID,
		ToolsConfig:    toolsConfig,
	}

	output, execErr := gw.Execute(ctx, req.Tool, req.Action, req.Input)

	w.Header().Set("Content-Type", "application/json")
	if execErr != nil {
		slog.Warn("ae_tool_execute failed", "employee_id", employeeID, "tool", req.Tool, "action", req.Action, "err", execErr)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output":  output,
			"success": false,
			"error":   execErr.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"output":  output,
		"success": true,
	})
}

// ListTools handles GET /api/ae/tools/list — returns available tool definitions
// for this AE based on their employee config.
func (h *AEAPIHandler) ListTools(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	ctx := r.Context()

	// Load employee config.
	var toolsConfig map[string]bool
	var employeeRole string
	var configJSON []byte
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT config FROM employee_configs WHERE employee_id = $1 LIMIT 1`,
		employeeID,
	).Scan(&configJSON)
	if err == nil {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(configJSON, &cfg); jsonErr == nil {
			toolsConfig = cfg.Tools
			employeeRole = cfg.Employee.Role
		}
	}

	// Build tool definitions based on what's available to this AE.
	defs := buildRemoteToolDefs(toolsConfig, employeeRole)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tools": defs})
}

// remoteToolDef is a tool definition for the tools list endpoint.
type remoteToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Tool        string         `json:"tool"`   // gateway tool name
	Action      string         `json:"action"` // gateway action name
}

// buildRemoteToolDefs returns tool definitions for all gateway tools available
// to an AE based on their config and role.
func buildRemoteToolDefs(toolsConfig map[string]bool, role string) []remoteToolDef {
	defs := []remoteToolDef{
		// Slack tools — always available
		{Name: "slack_post_message", Tool: "slack", Action: "post_message", Description: "Post a message to a Slack channel",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"channel": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"},
			}, "required": []string{"channel", "text"}}},
		{Name: "slack_reply_thread", Tool: "slack", Action: "reply_thread", Description: "Reply in a Slack thread",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"channel": map[string]any{"type": "string"}, "thread_ts": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"},
			}, "required": []string{"channel", "thread_ts", "text"}}},
		{Name: "slack_list_channels", Tool: "slack", Action: "list_channels", Description: "List all Slack channels",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},

		// GitHub tools — always available (create_comment needs approval)
		{Name: "github_list_prs", Tool: "github", Action: "list_prs", Description: "List pull requests in a repository",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"repo": map[string]any{"type": "string", "description": "owner/repo"},
				"state": map[string]any{"type": "string", "description": "open, closed, or all"},
			}, "required": []string{"repo"}}},
		{Name: "github_get_pr", Tool: "github", Action: "get_pr", Description: "Get details of a pull request",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"repo": map[string]any{"type": "string"}, "number": map[string]any{"type": "number"},
			}, "required": []string{"repo", "number"}}},

		// Workspace tools — always available
		{Name: "workspace_read_file", Tool: "workspace", Action: "read_file", Description: "Read a file from the shared workspace",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string"},
			}, "required": []string{"path"}}},
		{Name: "workspace_list_files", Tool: "workspace", Action: "list_files", Description: "List files in the shared workspace",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"company_id": map[string]any{"type": "string"},
			}}},
		{Name: "workspace_search_files", Tool: "workspace", Action: "search_files", Description: "Search files in the shared workspace by content",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"},
			}, "required": []string{"query"}}},
	}

	// Google Workspace tools — basic always available, write needs config flag
	defs = append(defs,
		remoteToolDef{Name: "google_workspace_list_emails", Tool: "google_workspace", Action: "list_emails", Description: "List emails from Gmail",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}, "max_results": map[string]any{"type": "number"},
			}}},
		remoteToolDef{Name: "google_workspace_send_email", Tool: "google_workspace", Action: "send_email", Description: "Send an email via Gmail",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"to": map[string]any{"type": "string"}, "subject": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"},
			}, "required": []string{"to", "subject", "body"}}},
	)

	// Figma tools — only if enabled in config
	if toolsConfig["figma"] {
		defs = append(defs,
			remoteToolDef{Name: "figma_list_files", Tool: "figma", Action: "list_files", Description: "List Figma files",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
				}}},
			remoteToolDef{Name: "figma_get_file", Tool: "figma", Action: "get_file", Description: "Get details of a Figma file",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"file_key": map[string]any{"type": "string"},
				}, "required": []string{"file_key"}}},
		)
	}

	return defs
}

// --- Phase 4: Work Tracking & Collaboration ---

// BacklogList handles GET /api/ae/backlog — returns the AE's work items.
func (h *AEAPIHandler) BacklogList(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	status := r.URL.Query().Get("status") // "todo", "in_progress", "blocked", or empty for all active
	ctx := r.Context()

	query := `SELECT id, COALESCE(parent_id,''), title, COALESCE(description,''), status, priority, COALESCE(source_task_id,''), updated_at
		FROM work_items WHERE owner_id = $1`
	args := []any{employeeID}

	if status != "" && status != "all" {
		query += ` AND status = $2`
		args = append(args, status)
	} else {
		query += ` AND status NOT IN ('done','cancelled')`
	}
	query += ` ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at ASC`

	rows, err := h.DB.Pool.Query(ctx, query, args...)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type item struct {
		ID           string `json:"id"`
		ParentID     string `json:"parent_id,omitempty"`
		Title        string `json:"title"`
		Description  string `json:"description,omitempty"`
		Status       string `json:"status"`
		Priority     string `json:"priority"`
		SourceTaskID string `json:"source_task_id,omitempty"`
		UpdatedAt    string `json:"updated_at"`
	}
	var items []item
	for rows.Next() {
		var it item
		var updatedAt time.Time
		if rows.Scan(&it.ID, &it.ParentID, &it.Title, &it.Description, &it.Status, &it.Priority, &it.SourceTaskID, &updatedAt) == nil {
			it.UpdatedAt = updatedAt.Format(time.RFC3339)
			items = append(items, it)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "count": len(items)})
}

// BacklogAdd handles POST /api/ae/backlog — creates a work item.
func (h *AEAPIHandler) BacklogAdd(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	var req struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Priority     string `json:"priority"`
		ParentID     string `json:"parent_id"`
		SourceTaskID string `json:"source_task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
		return
	}
	if req.Priority == "" {
		req.Priority = "medium"
	}

	id := newID()
	_, err := h.DB.Pool.Exec(ctx,
		`INSERT INTO work_items (id, parent_id, company_id, owner_id, title, description, status, priority, source_task_id)
		 VALUES ($1, NULLIF($2,''), $3, $4, $5, $6, 'todo', $7, NULLIF($8,''))`,
		id, req.ParentID, companyID, employeeID, req.Title, req.Description, req.Priority, req.SourceTaskID,
	)
	if err != nil {
		slog.Warn("backlog_add failed", "err", err)
		http.Error(w, `{"error":"failed to create work item"}`, http.StatusInternalServerError)
		return
	}

	// Record history.
	_, _ = h.DB.Pool.Exec(ctx,
		`INSERT INTO work_item_history (id, work_item_id, change_type, content) VALUES ($1, $2, 'created', $3)`,
		newID(), id, req.Title)

	slog.Info("backlog_add", "employee_id", employeeID, "item_id", id, "title", req.Title)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "title": req.Title, "status": "todo"})
}

// BacklogUpdate handles PATCH /api/ae/backlog/{id} — updates a work item.
func (h *AEAPIHandler) BacklogUpdate(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Status != "" {
		_, err := h.DB.Pool.Exec(ctx,
			`UPDATE work_items SET status = $1, updated_at = now() WHERE id = $2 AND owner_id = $3`,
			req.Status, itemID, employeeID)
		if err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
		_, _ = h.DB.Pool.Exec(ctx,
			`INSERT INTO work_item_history (id, work_item_id, change_type, content) VALUES ($1, $2, 'status_change', $3)`,
			newID(), itemID, req.Status)
	}

	if req.Note != "" {
		_, _ = h.DB.Pool.Exec(ctx,
			`INSERT INTO work_item_history (id, work_item_id, change_type, content) VALUES ($1, $2, 'note_added', $3)`,
			newID(), itemID, req.Note)
		_, _ = h.DB.Pool.Exec(ctx,
			`UPDATE work_items SET updated_at = now() WHERE id = $1`, itemID)
	}

	// Fetch updated item.
	var title, status string
	var updatedAt time.Time
	_ = h.DB.Pool.QueryRow(ctx,
		`SELECT title, status, updated_at FROM work_items WHERE id = $1`, itemID,
	).Scan(&title, &status, &updatedAt)

	slog.Info("backlog_update", "employee_id", employeeID, "item_id", itemID, "status", req.Status, "note_len", len(req.Note))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": itemID, "title": title, "status": status, "updated_at": updatedAt.Format(time.RFC3339),
	})
}

// Delegate handles POST /api/ae/delegate — creates work for another AE.
func (h *AEAPIHandler) Delegate(w http.ResponseWriter, r *http.Request) {
	fromID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	var req struct {
		TargetRole  string `json:"target_role"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Context     string `json:"context"`
		Priority    string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.TargetRole == "" {
		http.Error(w, `{"error":"target_role and title are required"}`, http.StatusBadRequest)
		return
	}
	if req.Priority == "" {
		req.Priority = "medium"
	}

	// Find target AE by role.
	var targetID, targetName string
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT id, name FROM employees WHERE company_id = $1 AND role = $2 AND type = 'ae' LIMIT 1`,
		companyID, req.TargetRole,
	).Scan(&targetID, &targetName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"no AE found with role %s"}`, req.TargetRole), http.StatusNotFound)
		return
	}

	// Create work item for target.
	itemID := newID()
	desc := req.Description
	if req.Context != "" {
		desc += "\n\n## Context from delegator\n" + req.Context
	}
	_, err = h.DB.Pool.Exec(ctx,
		`INSERT INTO work_items (id, company_id, owner_id, title, description, status, priority)
		 VALUES ($1, $2, $3, $4, $5, 'todo', $6)`,
		itemID, companyID, targetID, req.Title, desc, req.Priority,
	)
	if err != nil {
		http.Error(w, `{"error":"failed to create delegated work item"}`, http.StatusInternalServerError)
		return
	}

	// Also create a task row for web UI visibility.
	taskID := newID()
	_, _ = h.DB.Pool.Exec(ctx,
		`INSERT INTO tasks (id, company_id, title, description, assignee_id, status) VALUES ($1, $2, $3, $4, $5, 'open')`,
		taskID, companyID, req.Title, desc, targetID,
	)

	_, _ = h.DB.Pool.Exec(ctx,
		`INSERT INTO work_item_history (id, work_item_id, change_type, content, metadata) VALUES ($1, $2, 'created', $3, $4)`,
		newID(), itemID, "Delegated from "+fromID, fmt.Sprintf(`{"delegated_by":"%s"}`, fromID))

	slog.Info("delegate", "from", fromID, "to", targetID, "target_role", req.TargetRole, "title", req.Title)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"work_item_id": itemID, "task_id": taskID, "target": targetName, "status": "delegated",
	})
}

// Escalate handles POST /api/ae/escalate — flags an issue for human attention.
func (h *AEAPIHandler) Escalate(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	_ = r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	var req struct {
		Reason  string `json:"reason"`
		Context string `json:"context"`
		Urgency string `json:"urgency"` // low, medium, high
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Urgency == "" {
		req.Urgency = "medium"
	}

	// Get AE name for the Slack message.
	var aeName string
	_ = h.DB.Pool.QueryRow(ctx, `SELECT name FROM employees WHERE id = $1`, employeeID).Scan(&aeName)

	// Post to Slack if available.
	if h.SlackClient != nil {
		urgencyEmoji := map[string]string{"low": "ℹ️", "medium": "⚠️", "high": "🚨"}[req.Urgency]
		if urgencyEmoji == "" {
			urgencyEmoji = "⚠️"
		}
		msg := fmt.Sprintf("%s *Escalation from %s* [%s]\n> %s", urgencyEmoji, aeName, req.Urgency, req.Reason)
		if req.Context != "" {
			msg += "\n\nContext: " + req.Context
		}
		_, _ = h.SlackClient.PostMessage(ctx, "#general", msg)
	}

	slog.Info("escalate", "employee_id", employeeID, "urgency", req.Urgency, "reason", req.Reason)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "escalated", "urgency": req.Urgency})
}

// AESendMessage handles POST /api/ae/messages — sends a message to another AE.
func (h *AEAPIHandler) AESendMessage(w http.ResponseWriter, r *http.Request) {
	fromID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	var req struct {
		TargetRole string `json:"target_role"`
		Message    string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.TargetRole == "" || req.Message == "" {
		http.Error(w, `{"error":"target_role and message are required"}`, http.StatusBadRequest)
		return
	}

	var targetID string
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT id FROM employees WHERE company_id = $1 AND role = $2 AND type = 'ae' LIMIT 1`,
		companyID, req.TargetRole,
	).Scan(&targetID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"no AE found with role %s"}`, req.TargetRole), http.StatusNotFound)
		return
	}

	msgID := newID()
	_, err = h.DB.Pool.Exec(ctx,
		`INSERT INTO ae_messages (id, company_id, from_id, to_id, message) VALUES ($1, $2, $3, $4, $5)`,
		msgID, companyID, fromID, targetID, req.Message,
	)
	if err != nil {
		http.Error(w, `{"error":"failed to send message"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("ae_message", "from", fromID, "to", targetID, "role", req.TargetRole)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": msgID, "status": "sent", "target_role": req.TargetRole})
}

// UpdateTask handles PATCH /api/ae/tasks/{id} — lets AEs update their task status.
func (h *AEAPIHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	taskID := r.PathValue("id")
	ctx := r.Context()

	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Status != "" {
		_, err := h.DB.Pool.Exec(ctx,
			`UPDATE tasks SET status = $1 WHERE id = $2 AND assignee_id = $3`,
			req.Status, taskID, employeeID)
		if err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
	}

	slog.Info("ae_update_task", "employee_id", employeeID, "task_id", taskID, "status", req.Status)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": taskID, "status": req.Status})
}
