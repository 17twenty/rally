package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
)

// Service methods on AEAPIHandler — callable from both HTTP handlers and ChatHandler.
// These are the "business logic" extracted from the HTTP handler wrappers.

// TeamMember is a typed response for team queries.
type TeamMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// GetTeamMembers returns all employees for a company.
func (h *AEAPIHandler) GetTeamMembers(ctx context.Context, companyID string) ([]TeamMember, error) {
	employees, err := h.q().ListEmployeesByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var team []TeamMember
	for _, e := range employees {
		team = append(team, TeamMember{
			ID: e.ID, Name: db.Deref(e.Name), Role: e.Role,
			Type: e.Type, Status: e.Status,
		})
	}
	return team, nil
}

// BacklogItem is a typed response for backlog queries.
type BacklogItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	UpdatedAt string `json:"updated_at"`
}

// ListBacklog returns work items for an employee.
func (h *AEAPIHandler) ListBacklog(ctx context.Context, employeeID, status string) ([]BacklogItem, error) {
	rows, err := h.q().ListWorkItemsByOwner(ctx, employeeID)
	if err != nil {
		return nil, err
	}
	var items []BacklogItem
	for _, r := range rows {
		if status != "" && status != "all" && r.Status != status {
			continue
		}
		items = append(items, BacklogItem{
			ID: r.ID, Title: r.Title, Status: r.Status,
			Priority: r.Priority, UpdatedAt: db.PgTime(r.UpdatedAt).Format(time.RFC3339),
		})
	}
	return items, nil
}

// AddBacklogItem creates a new work item.
func (h *AEAPIHandler) AddBacklogItem(ctx context.Context, employeeID, companyID, title, description, priority string) (map[string]any, error) {
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if priority == "" {
		priority = "medium"
	}

	// Dedup check.
	existing, _ := h.q().CheckDuplicateWorkItem(ctx, dao.CheckDuplicateWorkItemParams{
		OwnerID: employeeID, CompanyID: companyID, Title: title,
	})
	if len(existing) > 0 {
		return map[string]any{
			"id": existing[0].ID, "title": existing[0].Title,
			"status": existing[0].Status, "note": "Work item already exists — reusing it.",
		}, nil
	}

	itemID := newID()
	row, err := h.q().CreateWorkItem(ctx, dao.CreateWorkItemParams{
		ID: itemID, CompanyID: companyID, OwnerID: employeeID,
		Title: title, Description: &description, Priority: priority,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": row.ID, "title": row.Title, "status": row.Status}, nil
}

// CreateTaskForRole creates a task and assigns it to an AE by role.
func (h *AEAPIHandler) CreateTaskForRole(ctx context.Context, companyID, title, description, assigneeRole string) (map[string]any, error) {
	// Find the AE by role.
	emp, err := h.q().GetEmployeeByRole(ctx, dao.GetEmployeeByRoleParams{
		CompanyID: companyID, Role: assigneeRole,
	})
	if err != nil {
		return nil, fmt.Errorf("no AE found with role %q", assigneeRole)
	}

	taskID := newID()
	_, err = h.q().CreateTask(ctx, dao.CreateTaskParams{
		ID: taskID, CompanyID: companyID, Title: title,
		Description: &description, AssigneeID: &emp.ID, Status: "open",
	})
	if err != nil {
		return nil, err
	}

	slog.Info("task_created", "task_id", taskID, "assignee", db.Deref(emp.Name), "role", assigneeRole)
	return map[string]any{
		"id": taskID, "title": title, "assignee": db.Deref(emp.Name), "status": "open",
	}, nil
}

// SendMessageToRole sends an inter-AE message to an employee by role.
func (h *AEAPIHandler) SendMessageToRole(ctx context.Context, fromID, companyID, targetRole, message string) (map[string]any, error) {
	emp, err := h.q().GetEmployeeByRole(ctx, dao.GetEmployeeByRoleParams{
		CompanyID: companyID, Role: targetRole,
	})
	if err != nil {
		return nil, fmt.Errorf("no AE found with role %q", targetRole)
	}

	msgID := newID()
	_, err = h.q().InsertAEMessage(ctx, dao.InsertAEMessageParams{
		ID: msgID, CompanyID: companyID, FromID: fromID,
		ToID: emp.ID, Message: message,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id": msgID, "status": "sent", "target": db.Deref(emp.Name),
	}, nil
}

// SearchMemoriesForEmployee searches memory events by keyword.
func (h *AEAPIHandler) SearchMemoriesForEmployee(ctx context.Context, employeeID, query string) ([]map[string]any, error) {
	results, err := h.q().SearchMemoryEvents(ctx, dao.SearchMemoryEventsParams{
		EmployeeID: employeeID, Column2: &query,
	})
	if err != nil {
		return nil, err
	}
	var mems []map[string]any
	for _, m := range results {
		mems = append(mems, map[string]any{
			"type": m.Type, "content": m.Content,
			"created_at": db.PgTime(m.CreatedAt).Format("2006-01-02 15:04"),
		})
	}
	return mems, nil
}

// ResolveCompanyID gets the company ID from an employee ID or rally-{companyID} prefix.
func (h *AEAPIHandler) ResolveCompanyID(ctx context.Context, employeeID string) string {
	if len(employeeID) > 6 && employeeID[:6] == "rally-" {
		return employeeID[6:]
	}
	if emp, err := h.q().GetEmployee(ctx, employeeID); err == nil {
		return emp.CompanyID
	}
	return ""
}

// ListProposedHiresForCompany returns all proposed hires.
func (h *AEAPIHandler) ListProposedHiresForCompany(ctx context.Context, companyID string) ([]map[string]any, error) {
	hires, err := h.q().ListProposedHiresByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for _, h := range hires {
		result = append(result, map[string]any{
			"role": h.Role, "status": h.Status, "rationale": db.Deref(h.Rationale),
		})
	}
	return result, nil
}

// ListAllActiveTasks returns all active tasks with assignee info.
func (h *AEAPIHandler) ListAllActiveTasks(ctx context.Context) ([]map[string]any, error) {
	tasks, err := h.q().ListActiveTasks(ctx)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for _, t := range tasks {
		result = append(result, map[string]any{
			"id": t.ID, "title": t.Title, "status": t.Status,
			"assignee_id": t.AssigneeID,
		})
	}
	return result, nil
}

// jsonResult is a helper for tool results.
func jsonResult(toolID string, data any) string {
	b, _ := json.Marshal(data)
	return string(b)
}
