package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/memory"
	"github.com/17twenty/rally/internal/queue"
	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/tools"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/internal/workspace"
)

// q returns a dao.Queries instance for the handler's DB pool.
func (h *AEAPIHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// ensureSlackClient loads the Slack token from vault if the client isn't set.
// This handles the case where Slack was connected via OAuth after server startup.
func (h *AEAPIHandler) ensureSlackClient(ctx context.Context) {
	if h.SlackClient == nil && h.Vault != nil {
		if token, err := h.Vault.Get(ctx, "rally-system", "slack"); err == nil && token != "" {
			h.SlackClient = slack.NewClient(token)
			slog.Info("slack: loaded bot token from vault (lazy init)")
		}
	}
}

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

	if err := h.q().UpdateEmployeeContainerStatus(r.Context(), dao.UpdateEmployeeContainerStatusParams{
		ID:              employeeID,
		ContainerStatus: db.Ref("running"),
	}); err != nil {
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
		Channel  string `json:"channel"`
		UserID   string `json:"user_id"`
		Text     string `json:"text"`
		ThreadTS string `json:"thread_ts"`
		TS       string `json:"ts"`
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

	// Fetch Slack messages, split into "new" and "context" based on the AE's
	// last-seen timestamp. The AE sends ?slack_since=<ts> to indicate when it
	// last checked. Events after that are "new" (act on these). Events before
	// are "context" (for continuity, don't re-act).
	//
	// We include bot messages so AEs can see each other's Slack posts.
	// The AE name is included so the client can filter out its own messages.
	var slackEvents []slackEventObs
	var slackContext []slackEventObs
	slackSince := r.URL.Query().Get("slack_since")

	allEvents, slackErr := h.q().GetRecentSlackEvents(ctx, dao.GetRecentSlackEventsParams{
		CompanyID: companyID, Limit: 20,
	})
	if slackErr == nil {
		h.ensureSlackClient(ctx)
		for _, e := range allEvents {
			text := db.Deref(e.Text)
			if text == "" {
				continue
			}
			userName := db.Deref(e.UserID)
			channelName := db.Deref(e.Channel)
			messageTs := db.Deref(e.MessageTs)
			if h.SlackClient != nil {
				userName = h.SlackClient.ResolveUserName(ctx, db.Deref(e.UserID))
				channelName = h.SlackClient.ResolveChannelName(ctx, db.Deref(e.Channel))
			}
			obs := slackEventObs{
				Channel:  channelName,
				UserID:   userName,
				Text:     text,
				ThreadTS: db.Deref(e.ThreadTs),
				TS:       messageTs,
			}
			if slackSince == "" || messageTs > slackSince {
				slackEvents = append(slackEvents, obs)
			} else {
				slackContext = append(slackContext, obs)
			}
		}
	}

	// Fetch all memory types — episodic (recent activity), reflections (learnings),
	// heuristics (rules). All feed back into the agent's context each cycle.
	var memories []memoryObs
	store := memory.NewMemoryStore(h.DB.Pool)
	recent, err := store.GetRecent(ctx, employeeID, 15) // all types, last 15
	if err == nil {
		for _, m := range recent {
			memories = append(memories, memoryObs{Type: m.Type, Content: m.Content})
		}
	}

	// Fetch active tasks assigned to this AE
	var tasks []taskObs
	daoTasks, err := h.q().ListTasksByAssignee(ctx, &employeeID)
	if err == nil {
		for _, t := range daoTasks {
			tasks = append(tasks, taskObs{
				ID:          t.ID,
				Title:       t.Title,
				Description: t.Description,
				Status:      t.Status,
			})
		}
	}

	// Fetch company policy
	policyDoc, _ := h.q().GetCompanyPolicy(ctx, companyID)

	// Fetch active work items (backlog) for this AE.
	type workItemObs struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	}
	var workItems []workItemObs
	daoWI, err := h.q().ListWorkItemsByOwner(ctx, employeeID)
	if err == nil {
		for _, wi := range daoWI {
			workItems = append(workItems, workItemObs{
				ID:       wi.ID,
				Title:    wi.Title,
				Status:   wi.Status,
				Priority: wi.Priority,
			})
		}
	}

	// Fetch unread messages from other AEs.
	type msgObs struct {
		FromID  string `json:"from_id"`
		Message string `json:"message"`
	}
	var messages []msgObs
	daoMsgs, err := h.q().ListUnreadMessages(ctx, employeeID)
	if err == nil {
		for _, m := range daoMsgs {
			messages = append(messages, msgObs{FromID: m.FromID, Message: m.Message})
		}
		// Mark as read.
		if len(messages) > 0 {
			_ = h.q().MarkMessagesAsRead(ctx, employeeID)
		}
	}

	// Fetch pending proposed hires for this company (so AEs don't re-propose).
	type proposedHireObs struct {
		Role      string `json:"role"`
		Status    string `json:"status"`
		Rationale string `json:"rationale"`
	}
	var proposedHires []proposedHireObs
	if hires, phErr := h.q().ListPendingHiresByCompany(ctx, companyID); phErr == nil {
		for _, ph := range hires {
			proposedHires = append(proposedHires, proposedHireObs{
				Role:      ph.Role,
				Status:    ph.Status,
				Rationale: db.Deref(ph.Rationale),
			})
		}
	}

	// Fetch company info.
	type companyObs struct {
		Name    string `json:"name"`
		Mission string `json:"mission"`
	}
	var company companyObs
	if c, cErr := h.q().GetCompany(ctx, companyID); cErr == nil {
		company = companyObs{Name: c.Name, Mission: db.Deref(c.Mission)}
	}

	// Load employee config for model_ref and soul.
	var modelRef string
	var soulMD string
	if ec, ecErr := h.q().GetEmployeeConfig(ctx, employeeID); ecErr == nil {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(ec.Config, &cfg); jsonErr == nil {
			if cfg.Cognition.DefaultModelRef != "" {
				modelRef = cfg.Cognition.DefaultModelRef
			}
			soulMD = cfg.Identity.SoulFile
		}
	}

	// Fetch team roster.
	type teamMemberObs struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	var team []teamMemberObs
	if emps, tErr := h.q().ListEmployeesByCompany(ctx, companyID); tErr == nil {
		for _, e := range emps {
			team = append(team, teamMemberObs{
				Name: db.Deref(e.Name), Role: e.Role, Type: e.Type, Status: e.Status,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"company":        company,
		"team":           team,
		"model_ref":      modelRef,
		"soul_md":        soulMD,
		"slack_events":   slackEvents,
		"slack_context":  slackContext,
		"memories":       memories,
		"tasks":          tasks,
		"work_items":     workItems,
		"messages":       messages,
		"proposed_hires": proposedHires,
		"policy_doc":     policyDoc,
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

	h.ensureSlackClient(r.Context())
	if h.SlackClient == nil {
		http.Error(w, `{"error":"slack not configured — connect via Settings page"}`, http.StatusServiceUnavailable)
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
	var saveErr error
	switch req.Type {
	case "reflection":
		saveErr = store.SaveReflection(r.Context(), req.EmployeeID, req.Content)
	case "heuristic":
		saveErr = store.SaveHeuristic(r.Context(), req.EmployeeID, req.Content)
	default:
		saveErr = store.SaveEpisodic(r.Context(), req.EmployeeID, req.Content, req.Metadata)
	}
	if saveErr != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, saveErr.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "stored"})
}

// SearchMemory handles GET /api/ae/memory/search — searches memory events by content.
func (h *AEAPIHandler) SearchMemory(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `{"error":"q parameter required"}`, http.StatusBadRequest)
		return
	}

	results, err := h.q().SearchMemoryEvents(r.Context(), dao.SearchMemoryEventsParams{
		EmployeeID: employeeID,
		Column2:    &query,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	type memoryResult struct {
		Type      string `json:"type"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}
	var items []memoryResult
	for _, m := range results {
		items = append(items, memoryResult{
			Type:    m.Type,
			Content: m.Content,
			CreatedAt: db.PgTime(m.CreatedAt).Format("2006-01-02 15:04"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"memories": items})
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

	_, err := h.q().InsertToolLog(r.Context(), dao.InsertToolLogParams{
		ID:         newID(),
		EmployeeID: req.EmployeeID,
		CompanyID:  db.Ref(companyID),
		Tool:       req.Tool,
		Action:     req.Action,
		Input:      inputJSON,
		Output:     outputJSON,
		Success:    req.Success,
		TraceID:    db.Ref(req.TraceID),
		TaskID:     nil,
		CreatedAt:  db.TimePg(time.Now()),
	})
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
	ecRow, err := h.q().GetEmployeeConfig(ctx, employeeID)
	if err == nil {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(ecRow.Config, &cfg); jsonErr == nil {
			toolsConfig = cfg.Tools
		}
	}

	// Ensure Slack client is available (may have been connected via OAuth after startup).
	h.ensureSlackClient(ctx)

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
	ecRow, err := h.q().GetEmployeeConfig(ctx, employeeID)
	if err == nil {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(ecRow.Config, &cfg); jsonErr == nil {
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
		// Slack read tools removed — Rally is the Slack gateway.
		// AEs see message text directly in observations from the DB.

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

	// Dedup: reject if same title+owner already exists and is not done.
	existing, _ := h.q().CheckDuplicateWorkItem(ctx, dao.CheckDuplicateWorkItemParams{
		OwnerID: employeeID, CompanyID: companyID, Title: req.Title,
	})
	if len(existing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": existing[0].ID, "title": existing[0].Title, "status": existing[0].Status,
			"note": "Work item already exists — reusing it.",
		})
		return
	}

	id := newID()
	_, err := h.q().CreateWorkItem(ctx, dao.CreateWorkItemParams{
		ID:           id,
		ParentID:     db.Ref(req.ParentID),
		CompanyID:    companyID,
		OwnerID:      employeeID,
		Title:        req.Title,
		Description:  db.Ref(req.Description),
		Status:       "todo",
		Priority:     req.Priority,
		SourceTaskID: db.Ref(req.SourceTaskID),
	})
	if err != nil {
		slog.Warn("backlog_add failed", "err", err)
		http.Error(w, `{"error":"failed to create work item"}`, http.StatusInternalServerError)
		return
	}

	// Record history.
	_ = h.q().AddWorkItemHistory(ctx, dao.AddWorkItemHistoryParams{
		ID:         newID(),
		WorkItemID: id,
		ChangeType: "created",
		Content:    &req.Title,
	})

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
		err := h.q().UpdateWorkItemStatus(ctx, dao.UpdateWorkItemStatusParams{
			ID:      itemID,
			Status:  req.Status,
			OwnerID: employeeID,
		})
		if err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
		_ = h.q().AddWorkItemHistory(ctx, dao.AddWorkItemHistoryParams{
			ID:         newID(),
			WorkItemID: itemID,
			ChangeType: "status_change",
			Content:    &req.Status,
		})
	}

	if req.Note != "" {
		_ = h.q().AddWorkItemHistory(ctx, dao.AddWorkItemHistoryParams{
			ID:         newID(),
			WorkItemID: itemID,
			ChangeType: "note_added",
			Content:    &req.Note,
		})
		_ = h.q().TouchWorkItem(ctx, itemID)
	}

	// Fetch updated item.
	wi, _ := h.q().GetWorkItem(ctx, itemID)
	title := wi.Title
	status := wi.Status
	updatedAt := db.PgTime(wi.UpdatedAt)

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
	targetEmp, err := h.q().GetEmployeeByRole(ctx, dao.GetEmployeeByRoleParams{
		CompanyID: companyID,
		Role:      req.TargetRole,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"no AE found with role %s"}`, req.TargetRole), http.StatusNotFound)
		return
	}
	targetID := targetEmp.ID
	targetName := db.Deref(targetEmp.Name)

	// Create work item for target.
	itemID := newID()
	desc := req.Description
	if req.Context != "" {
		desc += "\n\n## Context from delegator\n" + req.Context
	}
	_, err = h.q().CreateWorkItem(ctx, dao.CreateWorkItemParams{
		ID:        itemID,
		CompanyID: companyID,
		OwnerID:   targetID,
		Title:     req.Title,
		Description: db.Ref(desc),
		Status:    "todo",
		Priority:  req.Priority,
	})
	if err != nil {
		http.Error(w, `{"error":"failed to create delegated work item"}`, http.StatusInternalServerError)
		return
	}

	// Also create a task row for web UI visibility.
	taskID := newID()
	h.q().CreateTask(ctx, dao.CreateTaskParams{
		ID:          taskID,
		CompanyID:   companyID,
		Title:       req.Title,
		Description: db.Ref(desc),
		AssigneeID:  &targetID,
		Status:      "open",
	})

	delegatedContent := "Delegated from " + fromID
	_ = h.q().AddWorkItemHistory(ctx, dao.AddWorkItemHistoryParams{
		ID:         newID(),
		WorkItemID: itemID,
		ChangeType: "created",
		Content:    &delegatedContent,
		Metadata:   json.RawMessage(fmt.Sprintf(`{"delegated_by":"%s"}`, fromID)),
	})

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
	emp, _ := h.q().GetEmployee(ctx, employeeID)
	aeName := db.Deref(emp.Name)

	// Post escalation to Slack.
	h.ensureSlackClient(ctx)
	if h.SlackClient != nil {
		msg := fmt.Sprintf("*Escalation from %s* [%s]\n> %s", aeName, req.Urgency, req.Reason)
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

	targetEmp, err := h.q().GetEmployeeByRole(ctx, dao.GetEmployeeByRoleParams{
		CompanyID: companyID,
		Role:      req.TargetRole,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"no AE found with role %s"}`, req.TargetRole), http.StatusNotFound)
		return
	}
	targetID := targetEmp.ID

	msgID := newID()
	_, err = h.q().InsertAEMessage(ctx, dao.InsertAEMessageParams{
		ID:        msgID,
		CompanyID: companyID,
		FromID:    fromID,
		ToID:      targetID,
		Message:   req.Message,
	})
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
		err := h.q().UpdateTaskStatusByAssignee(ctx, dao.UpdateTaskStatusByAssigneeParams{
			ID:         taskID,
			Status:     req.Status,
			AssigneeID: &employeeID,
		})
		if err != nil {
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
			return
		}
	}

	// Auto-complete linked work items when task is marked done.
	if req.Status == "done" {
		_ = h.q().CompleteWorkItemsBySourceTask(ctx, &taskID)
	}

	slog.Info("ae_update_task", "employee_id", employeeID, "task_id", taskID, "status", req.Status)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": taskID, "status": req.Status})
}

// ProposeHire handles POST /api/ae/propose-hire — only the CEO can propose hires.
// Other AEs should use SendMessage or Delegate to request the CEO to hire.
func (h *AEAPIHandler) ProposeHire(w http.ResponseWriter, r *http.Request) {
	employeeID := r.Header.Get("X-AE-Employee-ID")
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	// Only CEO can propose hires. Other AEs must ask the CEO.
	emp, empErr := h.q().GetEmployee(ctx, employeeID)
	if empErr != nil {
		http.Error(w, `{"error":"employee not found"}`, http.StatusNotFound)
		return
	}
	if emp.Role != "CEO" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Only the CEO can propose hires. Use SendMessage to ask the CEO to hire for you.",
		})
		return
	}

	var req struct {
		Role       string `json:"role"`
		Department string `json:"department"`
		Rationale  string `json:"rationale"`
		ReportsTo  string `json:"reports_to"`
		Channel    string `json:"channel"` // Slack channel to post approval link to
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		http.Error(w, `{"error":"role is required"}`, http.StatusBadRequest)
		return
	}

	// Reject duplicate proposals — check if this role is already pending or approved.
	existingHires, _ := h.q().ListProposedHiresByCompany(ctx, companyID)
	for _, eh := range existingHires {
		if strings.EqualFold(eh.Role, req.Role) && (eh.Status == "pending" || eh.Status == "approved") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("A %s hire is already %s.", req.Role, eh.Status),
			})
			return
		}
	}

	hire, err := h.q().InsertProposedHire(ctx, dao.InsertProposedHireParams{
		ID:         newID(),
		CompanyID:  companyID,
		ProposedBy: employeeID,
		Role:       req.Role,
		Department: db.Ref(req.Department),
		Rationale:  db.Ref(req.Rationale),
		ReportsTo:  db.Ref(req.ReportsTo),
	})
	if err != nil {
		http.Error(w, `{"error":"failed to propose hire"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("propose_hire", "employee_id", employeeID, "role", req.Role, "department", req.Department)

	// DM the founder with an approval link via Rally's bot.
	// System notifications come from Rally directly, not from AEs in channels.
	h.ensureSlackClient(ctx)
	if h.SlackClient != nil {
		rallyURL := os.Getenv("RALLY_URL")
		if rallyURL == "" {
			rallyURL = "http://localhost:8432"
		}
		approveURL := fmt.Sprintf("%s/companies/%s", rallyURL, companyID)

		// Get AE name for context.
		proposerName := "An AE"
		if emp, empErr := h.q().GetEmployee(ctx, employeeID); empErr == nil {
			proposerName = db.Deref(emp.Name)
		}

		msg := fmt.Sprintf("*%s proposed a hire:* %s (%s)\n> %s\n<%s|Review and approve in Rally>",
			proposerName, req.Role, req.Department, req.Rationale, approveURL)

		// Post to the channel where the conversation happened (if provided),
		// AND to the founder's DM for visibility.
		if req.Channel != "" {
			_, _ = h.SlackClient.PostMessage(ctx, req.Channel, msg)
		}

		// Also DM the founder — find human employees and DM each.
		if humans, hErr := h.q().ListHumanEmployeesByCompany(ctx, companyID); hErr == nil {
			for _, h2 := range humans {
				if slackUID := db.Deref(h2.SlackUserID); slackUID != "" {
					_, _ = h.SlackClient.PostMessage(ctx, slackUID, msg)
				}
			}
		}

		slog.Info("propose_hire: approval notification sent", "role", req.Role)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":     hire.ID,
		"role":   hire.Role,
		"status": hire.Status,
	})
}

// ListTeam handles GET /api/ae/team — returns team members for this company.
func (h *AEAPIHandler) ListTeam(w http.ResponseWriter, r *http.Request) {
	companyID := r.Header.Get("X-AE-Company-ID")
	ctx := r.Context()

	rows, err := h.q().ListEmployeesByCompany(ctx, companyID)
	if err != nil {
		http.Error(w, `{"error":"failed to list team"}`, http.StatusInternalServerError)
		return
	}

	type teamMember struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}

	members := make([]teamMember, 0, len(rows))
	for _, e := range rows {
		members = append(members, teamMember{
			Name:   db.Deref(e.Name),
			Role:   e.Role,
			Type:   e.Type,
			Status: e.Status,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"team": members, "count": len(members)})
}

// ApproveHire handles POST /companies/{id}/hires/{hire_id}/approve — human approves a proposed hire.
func (h *AEAPIHandler) ApproveHire(w http.ResponseWriter, r *http.Request) {
	hireID := r.PathValue("hire_id")
	ctx := r.Context()

	hire, err := h.q().GetProposedHire(ctx, hireID)
	if err != nil {
		http.Error(w, `{"error":"hire not found"}`, http.StatusNotFound)
		return
	}

	if hire.Status != "pending" {
		http.Error(w, `{"error":"hire is not pending"}`, http.StatusConflict)
		return
	}

	// Mark as approved.
	_ = h.q().ApproveProposedHire(ctx, dao.ApproveProposedHireParams{
		ID:         hireID,
		ReviewedBy: db.Ref("human"),
	})

	// Enqueue a hiring job — or hire directly if queue isn't available.
	planRoleID := strings.ToLower(strings.ReplaceAll(hire.Role, " ", "-")) + "-ae"
	if queue.Client != nil {
		_, insertErr := queue.Client.Insert(ctx, queue.HiringJobArgs{
			CompanyID:  hire.CompanyID,
			PlanRoleID: planRoleID,
			Role:       hire.Role,
			Department: db.Deref(hire.Department),
			ReportsTo:  db.Deref(hire.ReportsTo),
			Rationale:  db.Deref(hire.Rationale),
		}, nil)
		if insertErr != nil {
			slog.Warn("approve_hire: queue insert failed, will try direct hiring", "err", insertErr)
		} else {
			slog.Info("approve_hire: hiring job enqueued", "role", hire.Role)
		}
	} else {
		slog.Warn("approve_hire: queue.Client is nil — this should not happen. Check InitQueue.")
	}

	slog.Info("approve_hire", "hire_id", hireID, "role", hire.Role)

	// Announce the hire in Slack — Rally (the system) makes announcements, not AEs.
	h.ensureSlackClient(ctx)
	if h.SlackClient != nil {
		// Find who proposed the hire.
		proposerName := "the team"
		if emp, empErr := h.q().GetEmployee(ctx, hire.ProposedBy); empErr == nil {
			proposerName = db.Deref(emp.Name)
		}

		msg := fmt.Sprintf("*Welcome aboard!* %s has been approved and is being onboarded now.\n> Proposed by %s: %s",
			hire.Role, proposerName, db.Deref(hire.Rationale))

		// Post to the first channel the bot is in.
		if channels, chErr := h.SlackClient.ListChannels(ctx); chErr == nil {
			for _, ch := range channels {
				if !ch.IsPrivate {
					_, _ = h.SlackClient.PostMessage(ctx, ch.ID, msg)
					break
				}
			}
		}
	}

	http.Redirect(w, r, "/companies/"+hire.CompanyID+"?msg=Approved+"+hire.Role+"+—+hiring+now", http.StatusSeeOther)
}

// RejectHire handles POST /companies/{id}/hires/{hire_id}/reject — human rejects a proposed hire.
func (h *AEAPIHandler) RejectHire(w http.ResponseWriter, r *http.Request) {
	hireID := r.PathValue("hire_id")
	ctx := r.Context()

	hire, err := h.q().GetProposedHire(ctx, hireID)
	if err != nil {
		http.Error(w, `{"error":"hire not found"}`, http.StatusNotFound)
		return
	}

	_ = h.q().RejectProposedHire(ctx, dao.RejectProposedHireParams{
		ID:         hireID,
		ReviewedBy: db.Ref("human"),
	})

	slog.Info("reject_hire", "hire_id", hireID, "role", hire.Role)

	http.Redirect(w, r, "/companies/"+hire.CompanyID+"?msg=Rejected+"+hire.Role, http.StatusSeeOther)
}
