package queue

// HeartbeatJobArgs is enqueued periodically for each AE to simulate activity.
type HeartbeatJobArgs struct {
	EmployeeID string `json:"employee_id"`
	CompanyID  string `json:"company_id"`
}

func (HeartbeatJobArgs) Kind() string { return "heartbeat" }

// SlackEventJobArgs is enqueued when a Slack event arrives for processing.
type SlackEventJobArgs struct {
	SlackEventID string `json:"slack_event_id"`
	CompanyID    string `json:"company_id"`
}

func (SlackEventJobArgs) Kind() string { return "slack_event" }

// ToolExecutionJobArgs is enqueued when an AE needs to execute a tool call.
type ToolExecutionJobArgs struct {
	EmployeeID string         `json:"employee_id"`
	Tool       string         `json:"tool"`
	Action     string         `json:"action"`
	Input      map[string]any `json:"input"`
	TraceID    string         `json:"trace_id"`
}

func (ToolExecutionJobArgs) Kind() string { return "tool_execution" }

// HiringJobArgs is enqueued when a company needs to onboard a new AE.
type HiringJobArgs struct {
	CompanyID  string `json:"company_id"`
	PlanRoleID string `json:"plan_role_id"`
	Role       string `json:"role"`
	Department string `json:"department"`
	ReportsTo  string `json:"reports_to"`
}

func (HiringJobArgs) Kind() string { return "hiring" }

// CampaignDraftJobArgs is enqueued when the CMO-AE needs to draft a marketing campaign.
type CampaignDraftJobArgs struct {
	CompanyID  string `json:"company_id"`
	EmployeeID string `json:"employee_id"`
	Brief      string `json:"brief"`
}

func (CampaignDraftJobArgs) Kind() string { return "campaign_draft" }

// ContentPublishJobArgs is enqueued when the CMO-AE needs to publish content to a Slack channel.
type ContentPublishJobArgs struct {
	CompanyID       string `json:"company_id"`
	EmployeeID      string `json:"employee_id"`
	WorkspaceFileID string `json:"workspace_file_id"`
	Channel         string `json:"channel"`
}

func (ContentPublishJobArgs) Kind() string { return "content_publish" }

// MemberJoinJobArgs is enqueued when a new human member joins the Slack workspace.
// CompanyID may be empty; the worker falls back to the first active company.
type MemberJoinJobArgs struct {
	CompanyID   string `json:"company_id"`
	SlackUserID string `json:"slack_user_id"`
	RealName    string `json:"real_name"`
}

func (MemberJoinJobArgs) Kind() string { return "member_join" }
