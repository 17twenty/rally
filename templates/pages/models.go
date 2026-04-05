package pages

import (
	"time"

	"github.com/17twenty/rally/internal/domain"
)

// TeamWorkItem is a work item with the owner's name for dashboard display.
type TeamWorkItem struct {
	OwnerName string
	OwnerRole string
	Title     string
	Status    string
	Priority  string
	UpdatedAt time.Time
}

// DashboardData holds all data for the main dashboard page.
type DashboardData struct {
	TotalAEs       int
	TotalTasks     int
	TotalKBEntries int
	AgentCards     []AgentCard
	RecentLogs     []ToolLogRow
	TeamWork       []TeamWorkItem
}

// AgentCard represents a single employee's status card (used for dashboard and agent list).
type AgentCard struct {
	Employee   domain.Employee
	LastActive time.Time
}

// ToolLogRow is a display-friendly flattened view of a tool_logs row.
type ToolLogRow struct {
	ID           string
	EmployeeID   string
	EmployeeName string
	Tool         string
	Action       string
	Success      bool
	TraceID      string
	TaskID       string
	CreatedAt    time.Time
}

// AgentsPageData holds data for the agents list page.
type AgentsPageData struct {
	Agents []AgentCard
}

// WorkItemRow is a display-friendly view of a work item.
type WorkItemRow struct {
	ID        string
	Title     string
	Status    string
	Priority  string
	UpdatedAt time.Time
}

// AgentDetailData holds data for the single-agent detail page.
type AgentDetailData struct {
	Employee     domain.Employee
	SoulContent  string
	ConfigYAML   string
	WorkItems    []WorkItemRow
	MemoryEvents []domain.MemoryEvent
	RecentLogs   []ToolLogRow
}

// ProposedHireRow is a display-friendly view of a proposed hire.
type ProposedHireRow struct {
	ID           string
	Role         string
	Department   string
	Rationale    string
	ReportsTo    string
	Status       string
	ProposerName string
	CreatedAt    time.Time
}

// SettingsData holds data for the settings page (shown when company exists).
type SettingsData struct {
	Company        domain.Company
	SlackConnected bool
}

// LogsPageData holds data for the log viewer page.
type LogsPageData struct {
	Logs           []ToolLogRow
	Employees      []domain.Employee
	FilterEmployee string
	FilterTool     string
}

// TaskRow is a display-friendly view of a task with its assignee name.
type TaskRow struct {
	Task         domain.Task
	AssigneeName string
	CompanyName  string
}

// TasksPageData holds data for the tasks list page.
type TasksPageData struct {
	Tasks        []TaskRow
	Companies    []domain.Company
	FilterStatus    string
	FilterCompanyID string
}

// TaskDetailData holds data for the task detail page.
type TaskDetailData struct {
	Task         domain.Task
	AssigneeName string
	CompanyName  string
}

// TaskNewPageData holds data for the task creation form.
type TaskNewPageData struct {
	Companies []domain.Company
	Employees []domain.Employee
}

