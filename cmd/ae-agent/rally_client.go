package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RallyClient communicates with Rally's AE API.
type RallyClient struct {
	baseURL    string
	token      string
	employeeID string
	companyID  string
	http       *http.Client
}

// NewRallyClient creates a new client for the Rally AE API.
func NewRallyClient(baseURL, token, employeeID, companyID string) *RallyClient {
	return &RallyClient{
		baseURL:    baseURL,
		token:      token,
		employeeID: employeeID,
		companyID:  companyID,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *RallyClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rally API %s %s: %d %s", method, path, resp.StatusCode, string(respData))
	}
	return respData, nil
}

// Register announces the AE agent is alive to Rally.
func (c *RallyClient) Register(ctx context.Context) error {
	_, err := c.do(ctx, "POST", "/api/ae/register", map[string]string{
		"employee_id": c.employeeID,
		"company_id":  c.companyID,
	})
	return err
}

// Heartbeat reports the agent is alive with its current cycle count.
func (c *RallyClient) Heartbeat(ctx context.Context, cycle int) error {
	_, err := c.do(ctx, "POST", "/api/ae/heartbeat", map[string]any{
		"employee_id": c.employeeID,
		"cycle":       cycle,
	})
	return err
}

// Observations represents what the AE should observe this cycle.
type Observations struct {
	Company       CompanyObs        `json:"company"`
	Team          []TeamMemberObs   `json:"team"`
	ModelRef      string            `json:"model_ref"`  // server-side model override
	SoulMD        string            `json:"soul_md"`    // identity from DB (single source of truth)
	SlackEvents   []SlackEventObs   `json:"slack_events"`
	SlackContext  []SlackEventObs   `json:"slack_context"`
	Memories      []MemoryObs       `json:"memories"`
	Tasks         []TaskObs         `json:"tasks"`
	WorkItems     []WorkItemObs     `json:"work_items"`
	Messages      []AEMessageObs    `json:"messages"`
	ProposedHires []ProposedHireObs `json:"proposed_hires"`
	PolicyDoc     string            `json:"policy_doc"`
}

// CompanyObs is company info returned by the observations endpoint.
type CompanyObs struct {
	Name    string `json:"name"`
	Mission string `json:"mission"`
}

// TeamMemberObs is a team member returned by the observations endpoint.
type TeamMemberObs struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// ProposedHireObs is a pending hire proposal visible in observations.
type ProposedHireObs struct {
	Role      string `json:"role"`
	Status    string `json:"status"`
	Rationale string `json:"rationale"`
}

// WorkItemObs is a backlog work item returned by the observations endpoint.
type WorkItemObs struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

// AEMessageObs is an inter-AE message returned by the observations endpoint.
type AEMessageObs struct {
	FromID  string `json:"from_id"`
	Message string `json:"message"`
}

// SlackEventObs is a Slack message from the observations endpoint.
// Rally is the Slack gateway — AEs see message text directly, no API calls needed.
type SlackEventObs struct {
	Channel  string `json:"channel"`
	UserID   string `json:"user_id"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts"`
	TS       string `json:"ts"`
}

// MemoryObs is a memory event returned by the observations endpoint.
type MemoryObs struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// TaskObs is a task returned by the observations endpoint.
type TaskObs struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// FetchObservations retrieves current observations for this AE.
func (c *RallyClient) FetchObservations(ctx context.Context, slackSince string) (*Observations, error) {
	url := fmt.Sprintf("/api/ae/observations?employee_id=%s", c.employeeID)
	if slackSince != "" {
		url += "&slack_since=" + slackSince
	}
	data, err := c.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	var obs Observations
	if err := json.Unmarshal(data, &obs); err != nil {
		return nil, err
	}
	return &obs, nil
}

// LLMCompleteRequest is the request body for the legacy LLM completion endpoint.
type LLMCompleteRequest struct {
	ModelRef     string `json:"model_ref"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	MaxTokens    int    `json:"max_tokens"`
}

// CompleteLLM proxies an LLM completion through Rally's router (legacy).
func (c *RallyClient) CompleteLLM(ctx context.Context, req LLMCompleteRequest) (string, error) {
	data, err := c.do(ctx, "POST", "/api/ae/llm/complete", req)
	if err != nil {
		return "", err
	}
	var resp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	return resp.Response, nil
}

// --- Multi-turn tool-use types (agent-side mirrors of llm.* types) ---

// ChatMessage is the agent-side representation of a conversation message.
type ChatMessage struct {
	Role        string           `json:"role"`
	Content     string           `json:"content,omitempty"`
	ToolCalls   []ChatToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ChatToolResult `json:"tool_results,omitempty"`
}

// ChatToolCall represents the LLM requesting a tool invocation.
type ChatToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ChatToolResult represents the outcome of executing a tool call.
type ChatToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// ChatLLMRequest is the request body for the multi-turn tool-use LLM endpoint.
type ChatLLMRequest struct {
	ModelRef  string           `json:"model_ref"`
	Messages  []ChatMessage    `json:"messages"`
	Tools     []ToolDefinition `json:"tools"`
	MaxTokens int              `json:"max_tokens"`
}

// ChatLLMResult is the response from the multi-turn tool-use LLM endpoint.
type ChatLLMResult struct {
	Message    ChatMessage `json:"message"`
	StopReason string      `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ChatLLM calls Rally's /api/ae/llm/chat endpoint for multi-turn tool-use completion.
func (c *RallyClient) ChatLLM(ctx context.Context, req ChatLLMRequest) (*ChatLLMResult, error) {
	if req.ModelRef == "" {
		req.ModelRef = "greenthread-gpt-oss-120b"
	}
	data, err := c.do(ctx, "POST", "/api/ae/llm/chat", req)
	if err != nil {
		return nil, err
	}
	var result ChatLLMResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse chat response: %w", err)
	}
	return &result, nil
}

// SendSlack sends a message to a Slack channel via Rally.
func (c *RallyClient) SendSlack(ctx context.Context, channel, text string) error {
	_, err := c.do(ctx, "POST", "/api/ae/slack/send", map[string]string{
		"channel": channel,
		"text":    text,
	})
	return err
}

// StoreMemory saves an episodic memory event.
func (c *RallyClient) StoreMemory(ctx context.Context, memType, content string, metadata map[string]any) error {
	_, err := c.do(ctx, "POST", "/api/ae/memory", map[string]any{
		"employee_id": c.employeeID,
		"type":        memType,
		"content":     content,
		"metadata":    metadata,
	})
	return err
}

// SearchMemories searches stored memory events by content keyword.
func (c *RallyClient) SearchMemories(ctx context.Context, query string) ([]byte, error) {
	return c.do(ctx, "GET", fmt.Sprintf("/api/ae/memory/search?q=%s", query), nil)
}

// ListCredentials returns available credential providers (no tokens).
func (c *RallyClient) ListCredentials(ctx context.Context) ([]byte, error) {
	return c.do(ctx, "GET", "/api/ae/credentials", nil)
}

// StoreCredential saves a credential to Rally's vault.
func (c *RallyClient) StoreCredential(ctx context.Context, provider, token, accessType string, scopes []string) error {
	_, err := c.do(ctx, "POST", "/api/ae/credentials", map[string]any{
		"provider": provider, "token": token, "access_type": accessType, "scopes": scopes,
	})
	return err
}

// FetchCredential retrieves a credential from Rally's vault.
func (c *RallyClient) FetchCredential(ctx context.Context, provider string) (string, error) {
	data, err := c.do(ctx, "GET", fmt.Sprintf("/api/ae/credentials/%s", provider), nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	return result.Token, nil
}

// SubmitLog records a tool execution log.
func (c *RallyClient) SubmitLog(ctx context.Context, tool, action string, input, output map[string]any, success bool, traceID string) error {
	_, err := c.do(ctx, "POST", "/api/ae/logs", map[string]any{
		"employee_id": c.employeeID,
		"tool":        tool,
		"action":      action,
		"input":       input,
		"output":      output,
		"success":     success,
		"trace_id":    traceID,
	})
	return err
}

// ExecuteRemoteTool calls a gateway tool via Rally's /api/ae/tools/execute endpoint.
func (c *RallyClient) ExecuteRemoteTool(ctx context.Context, tool, action string, input map[string]any) (map[string]any, error) {
	data, err := c.do(ctx, "POST", "/api/ae/tools/execute", map[string]any{
		"tool":   tool,
		"action": action,
		"input":  input,
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Output  map[string]any `json:"output"`
		Success bool           `json:"success"`
		Error   string         `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse tool response: %w", err)
	}
	if !resp.Success {
		return resp.Output, fmt.Errorf("remote tool %s.%s: %s", tool, action, resp.Error)
	}
	return resp.Output, nil
}

// RemoteToolDef is a tool definition returned by the tools/list endpoint.
type RemoteToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Tool        string         `json:"tool"`
	Action      string         `json:"action"`
}

// FetchToolDefinitions retrieves the list of remote tools available to this AE.
func (c *RallyClient) FetchToolDefinitions(ctx context.Context) ([]RemoteToolDef, error) {
	data, err := c.do(ctx, "GET", "/api/ae/tools/list", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []RemoteToolDef `json:"tools"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}
	return resp.Tools, nil
}

// --- Backlog & Collaboration ---

// BacklogList fetches the AE's active work items.
func (c *RallyClient) BacklogList(ctx context.Context, status string) ([]byte, error) {
	path := "/api/ae/backlog"
	if status != "" {
		path += "?status=" + status
	}
	return c.do(ctx, "GET", path, nil)
}

// BacklogAdd creates a new work item.
func (c *RallyClient) BacklogAdd(ctx context.Context, title, description, priority, parentID, sourceTaskID string) ([]byte, error) {
	return c.do(ctx, "POST", "/api/ae/backlog", map[string]string{
		"title": title, "description": description, "priority": priority,
		"parent_id": parentID, "source_task_id": sourceTaskID,
	})
}

// BacklogUpdate updates a work item's status or adds a note.
func (c *RallyClient) BacklogUpdate(ctx context.Context, id, status, note string) ([]byte, error) {
	return c.do(ctx, "PATCH", "/api/ae/backlog/"+id, map[string]string{
		"status": status, "note": note,
	})
}

// DelegateWork delegates a work item to another AE by role.
func (c *RallyClient) DelegateWork(ctx context.Context, targetRole, title, description, taskContext, priority string) ([]byte, error) {
	return c.do(ctx, "POST", "/api/ae/delegate", map[string]string{
		"target_role": targetRole, "title": title, "description": description,
		"context": taskContext, "priority": priority,
	})
}

// EscalateToHuman escalates an issue for human attention.
func (c *RallyClient) EscalateToHuman(ctx context.Context, reason, taskContext, urgency string) ([]byte, error) {
	return c.do(ctx, "POST", "/api/ae/escalate", map[string]string{
		"reason": reason, "context": taskContext, "urgency": urgency,
	})
}

// SendAEMessage sends a message to another AE.
func (c *RallyClient) SendAEMessage(ctx context.Context, targetRole, message string) ([]byte, error) {
	return c.do(ctx, "POST", "/api/ae/messages", map[string]string{
		"target_role": targetRole, "message": message,
	})
}

// UpdateTaskStatus updates a task's status.
func (c *RallyClient) UpdateTaskStatus(ctx context.Context, taskID, status, note string) ([]byte, error) {
	return c.do(ctx, "PATCH", "/api/ae/tasks/"+taskID, map[string]string{
		"status": status, "note": note,
	})
}

// ProposeHire proposes a new team member to be hired.
func (c *RallyClient) ProposeHire(ctx context.Context, role, department, rationale, reportsTo, channel string) ([]byte, error) {
	return c.do(ctx, "POST", "/api/ae/propose-hire", map[string]string{
		"role": role, "department": department, "rationale": rationale, "reports_to": reportsTo, "channel": channel,
	})
}

// ListTeam returns all team members for this AE's company.
func (c *RallyClient) ListTeam(ctx context.Context) ([]byte, error) {
	return c.do(ctx, "GET", "/api/ae/team", nil)
}

