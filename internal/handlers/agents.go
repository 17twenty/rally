package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/templates/pages"
)

// AgentHandler handles agent list, detail, and log pages.
type AgentHandler struct {
	DB *db.DB
}

func (h *AgentHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// List handles GET /agents.
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	data := pages.AgentsPageData{}

	if h.DB != nil {
		ctx := r.Context()
		rows, err := h.q().ListAllEmployeesWithLastActive(ctx)
		if err == nil {
			for _, row := range rows {
				ac := pages.AgentCard{
					Employee: daoAERowToEmployee(row.ID, row.CompanyID, row.Name, row.Role,
						row.Specialties, row.Type, row.Status, row.SlackUserID, db.PgTime(row.CreatedAt)),
				}
				if t, ok := row.LastActive.(time.Time); ok {
					ac.LastActive = t
				}
				data.Agents = append(data.Agents, ac)
			}
		}
	}

	templ.Handler(pages.Agents(data)).ServeHTTP(w, r)
}

// Detail handles GET /agents/{id}.
func (h *AgentHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.DB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	data := pages.AgentDetailData{}

	// Load employee
	emp, err := h.q().GetEmployee(ctx, id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	data.Employee = domain.Employee{
		ID:          emp.ID,
		CompanyID:   emp.CompanyID,
		Name:        db.Deref(emp.Name),
		Role:        emp.Role,
		Specialties: db.Deref(emp.Specialties),
		Type:        emp.Type,
		Status:      emp.Status,
		SlackUserID: db.Deref(emp.SlackUserID),
		CreatedAt:   db.PgTime(emp.CreatedAt),
	}

	// Load config — extract soul content and format as YAML
	ecRow, err := h.q().GetEmployeeConfig(ctx, id)
	if err == nil && len(ecRow.Config) > 0 {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(ecRow.Config, &cfg); jsonErr == nil {
			data.SoulContent = cfg.Identity.SoulFile
		}
		var raw map[string]any
		if jsonErr := json.Unmarshal(ecRow.Config, &raw); jsonErr == nil {
			if yamlBytes, yamlErr := yaml.Marshal(raw); yamlErr == nil {
				data.ConfigYAML = string(yamlBytes)
			}
		}
	}

	// Load active work items
	wiRows, err := h.q().ListWorkItemsByOwnerNotCancelled(ctx, id)
	if err == nil {
		for _, row := range wiRows {
			data.WorkItems = append(data.WorkItems, pages.WorkItemRow{
				ID:        row.ID,
				Title:     row.Title,
				Status:    row.Status,
				Priority:  row.Priority,
				UpdatedAt: db.PgTime(row.UpdatedAt),
			})
		}
	}

	// Load recent memory events
	memRows, err := h.q().GetRecentMemoryEvents(ctx, dao.GetRecentMemoryEventsParams{
		EmployeeID: id,
		Limit:      10,
	})
	if err == nil {
		for _, row := range memRows {
			data.MemoryEvents = append(data.MemoryEvents, domain.MemoryEvent{
				ID:         row.ID,
				EmployeeID: row.EmployeeID,
				Type:       row.Type,
				Content:    row.Content,
				CreatedAt:  db.PgTime(row.CreatedAt),
			})
		}
	}

	// Load recent tool logs
	tlRows, err := h.q().GetToolLogsByEmployee(ctx, dao.GetToolLogsByEmployeeParams{
		EmployeeID: id,
		Limit:      20,
	})
	if err == nil {
		empName := data.Employee.Name
		if empName == "" {
			empName = data.Employee.Role
		}
		for _, row := range tlRows {
			data.RecentLogs = append(data.RecentLogs, pages.ToolLogRow{
				ID:           row.ID,
				EmployeeID:   row.EmployeeID,
				EmployeeName: empName,
				Tool:         row.Tool,
				Action:       row.Action,
				Success:      row.Success,
				TraceID:      db.Deref(row.TraceID),
				TaskID:       db.Deref(row.TaskID),
				CreatedAt:    db.PgTime(row.CreatedAt),
			})
		}
	}

	templ.Handler(pages.AgentDetail(data)).ServeHTTP(w, r)
}

// Logs handles GET /logs.
func (h *AgentHandler) Logs(w http.ResponseWriter, r *http.Request) {
	data := pages.LogsPageData{
		FilterEmployee: r.URL.Query().Get("employee"),
		FilterTool:     r.URL.Query().Get("tool"),
	}

	if h.DB != nil {
		ctx := r.Context()

		// Employees for filter dropdown
		empRows, err := h.q().ListAllEmployees(ctx)
		if err == nil {
			for _, row := range empRows {
				data.Employees = append(data.Employees, domain.Employee{
					ID:          row.ID,
					CompanyID:   row.CompanyID,
					Name:        db.Deref(row.Name),
					Role:        row.Role,
					Specialties: db.Deref(row.Specialties),
					Type:        row.Type,
					Status:      row.Status,
					SlackUserID: db.Deref(row.SlackUserID),
					CreatedAt:   db.PgTime(row.CreatedAt),
				})
			}
		}

		// Fetch filtered tool logs using the appropriate dao query
		type logRow struct {
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
		var logs []logRow

		switch {
		case data.FilterEmployee != "" && data.FilterTool != "":
			rows, qErr := h.q().ListToolLogsWithEmployeeByEmployeeAndTool(ctx, dao.ListToolLogsWithEmployeeByEmployeeAndToolParams{
				EmployeeID: data.FilterEmployee,
				Tool:       "%" + data.FilterTool + "%",
			})
			if qErr == nil {
				for _, r := range rows {
					logs = append(logs, logRow{r.ID, r.EmployeeID, r.EmployeeName, r.Tool, r.Action, r.Success, r.TraceID, r.TaskID, db.PgTime(r.CreatedAt)})
				}
			}
		case data.FilterEmployee != "":
			rows, qErr := h.q().ListToolLogsWithEmployeeByEmployee(ctx, data.FilterEmployee)
			if qErr == nil {
				for _, r := range rows {
					logs = append(logs, logRow{r.ID, r.EmployeeID, r.EmployeeName, r.Tool, r.Action, r.Success, r.TraceID, r.TaskID, db.PgTime(r.CreatedAt)})
				}
			}
		case data.FilterTool != "":
			rows, qErr := h.q().ListToolLogsWithEmployeeByTool(ctx, "%"+data.FilterTool+"%")
			if qErr == nil {
				for _, r := range rows {
					logs = append(logs, logRow{r.ID, r.EmployeeID, r.EmployeeName, r.Tool, r.Action, r.Success, r.TraceID, r.TaskID, db.PgTime(r.CreatedAt)})
				}
			}
		default:
			rows, qErr := h.q().ListToolLogsWithEmployeeAll(ctx)
			if qErr == nil {
				for _, r := range rows {
					logs = append(logs, logRow{r.ID, r.EmployeeID, r.EmployeeName, r.Tool, r.Action, r.Success, r.TraceID, r.TaskID, db.PgTime(r.CreatedAt)})
				}
			}
		}

		for _, l := range logs {
			data.Logs = append(data.Logs, pages.ToolLogRow{
				ID:           l.ID,
				EmployeeID:   l.EmployeeID,
				EmployeeName: l.EmployeeName,
				Tool:         l.Tool,
				Action:       l.Action,
				Success:      l.Success,
				TraceID:      l.TraceID,
				TaskID:       l.TaskID,
				CreatedAt:    l.CreatedAt,
			})
		}
	}

	templ.Handler(pages.Logs(data)).ServeHTTP(w, r)
}
