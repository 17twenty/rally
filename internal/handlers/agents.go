package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/templates/pages"
)

// AgentHandler handles agent list, detail, and log pages.
type AgentHandler struct {
	DB *db.DB
}

// List handles GET /agents.
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	data := pages.AgentsPageData{}

	if h.DB != nil {
		ctx := r.Context()
		rows, err := h.DB.Pool.Query(ctx, `
			SELECT e.id, e.company_id, COALESCE(e.name,''), e.role, COALESCE(e.specialties,''),
			       e.type, e.status, COALESCE(e.slack_user_id,''), e.created_at,
			       MAX(tl.created_at) as last_active
			FROM employees e
			LEFT JOIN tool_logs tl ON tl.employee_id = e.id
			GROUP BY e.id, e.company_id, e.name, e.role, e.specialties, e.type, e.status, e.slack_user_id, e.created_at
			ORDER BY e.type, e.created_at DESC
		`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ac pages.AgentCard
				var lastActive *time.Time
				if scanErr := rows.Scan(
					&ac.Employee.ID, &ac.Employee.CompanyID, &ac.Employee.Name, &ac.Employee.Role,
					&ac.Employee.Specialties, &ac.Employee.Type, &ac.Employee.Status,
					&ac.Employee.SlackUserID, &ac.Employee.CreatedAt, &lastActive,
				); scanErr == nil {
					if lastActive != nil {
						ac.LastActive = *lastActive
					}
					data.Agents = append(data.Agents, ac)
				}
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
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
		 FROM employees WHERE id = $1`, id,
	).Scan(
		&data.Employee.ID, &data.Employee.CompanyID, &data.Employee.Name, &data.Employee.Role,
		&data.Employee.Specialties, &data.Employee.Type, &data.Employee.Status,
		&data.Employee.SlackUserID, &data.Employee.CreatedAt,
	)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	// Load config — extract soul content and format as YAML
	var configJSON []byte
	if scanErr := h.DB.Pool.QueryRow(ctx,
		`SELECT config FROM employee_configs WHERE employee_id = $1 ORDER BY created_at DESC LIMIT 1`, id,
	).Scan(&configJSON); scanErr == nil && len(configJSON) > 0 {
		var cfg domain.EmployeeConfig
		if jsonErr := json.Unmarshal(configJSON, &cfg); jsonErr == nil {
			data.SoulContent = cfg.Identity.SoulFile
		}
		var raw map[string]any
		if jsonErr := json.Unmarshal(configJSON, &raw); jsonErr == nil {
			if yamlBytes, yamlErr := yaml.Marshal(raw); yamlErr == nil {
				data.ConfigYAML = string(yamlBytes)
			}
		}
	}

	// Load active work items
	wiRows, err := h.DB.Pool.Query(ctx,
		`SELECT id, title, status, priority, updated_at FROM work_items
		 WHERE owner_id = $1 AND status NOT IN ('cancelled')
		 ORDER BY CASE status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 WHEN 'todo' THEN 2 ELSE 3 END,
		 CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		 updated_at DESC LIMIT 20`, id)
	if err == nil {
		defer wiRows.Close()
		for wiRows.Next() {
			var wi pages.WorkItemRow
			if scanErr := wiRows.Scan(&wi.ID, &wi.Title, &wi.Status, &wi.Priority, &wi.UpdatedAt); scanErr == nil {
				data.WorkItems = append(data.WorkItems, wi)
			}
		}
	}

	// Load recent memory events
	memRows, err := h.DB.Pool.Query(ctx,
		`SELECT id, employee_id, type, content, created_at FROM memory_events
		 WHERE employee_id = $1 ORDER BY created_at DESC LIMIT 10`, id)
	if err == nil {
		defer memRows.Close()
		for memRows.Next() {
			var m domain.MemoryEvent
			if scanErr := memRows.Scan(&m.ID, &m.EmployeeID, &m.Type, &m.Content, &m.CreatedAt); scanErr == nil {
				data.MemoryEvents = append(data.MemoryEvents, m)
			}
		}
	}

	// Load recent tool logs
	logRows, err := h.DB.Pool.Query(ctx,
		`SELECT id, employee_id, tool, action, success, COALESCE(trace_id,''), COALESCE(task_id,''), created_at
		 FROM tool_logs WHERE employee_id = $1 ORDER BY created_at DESC LIMIT 20`, id)
	if err == nil {
		defer logRows.Close()
		empName := data.Employee.Name
		if empName == "" {
			empName = data.Employee.Role
		}
		for logRows.Next() {
			var l pages.ToolLogRow
			if scanErr := logRows.Scan(
				&l.ID, &l.EmployeeID, &l.Tool, &l.Action, &l.Success, &l.TraceID, &l.TaskID, &l.CreatedAt,
			); scanErr == nil {
				l.EmployeeName = empName
				data.RecentLogs = append(data.RecentLogs, l)
			}
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
		empRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
			 FROM employees ORDER BY type, role`)
		if err == nil {
			defer empRows.Close()
			for empRows.Next() {
				var e domain.Employee
				if scanErr := empRows.Scan(
					&e.ID, &e.CompanyID, &e.Name, &e.Role, &e.Specialties, &e.Type, &e.Status, &e.SlackUserID, &e.CreatedAt,
				); scanErr == nil {
					data.Employees = append(data.Employees, e)
				}
			}
		}

		// Build filtered query
		query := `
			SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id),
			       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,''), COALESCE(tl.task_id,''), tl.created_at
			FROM tool_logs tl
			LEFT JOIN employees e ON e.id = tl.employee_id
			WHERE 1=1`
		args := []any{}
		argIdx := 1
		if data.FilterEmployee != "" {
			query += fmt.Sprintf(" AND tl.employee_id = $%d", argIdx)
			args = append(args, data.FilterEmployee)
			argIdx++
		}
		if data.FilterTool != "" {
			query += fmt.Sprintf(" AND tl.tool ILIKE $%d", argIdx)
			args = append(args, "%"+data.FilterTool+"%")
			argIdx++
		}
		_ = argIdx
		query += " ORDER BY tl.created_at DESC LIMIT 200"

		logRows, err := h.DB.Pool.Query(ctx, query, args...)
		if err == nil {
			defer logRows.Close()
			for logRows.Next() {
				var l pages.ToolLogRow
				if scanErr := logRows.Scan(
					&l.ID, &l.EmployeeID, &l.EmployeeName,
					&l.Tool, &l.Action, &l.Success, &l.TraceID, &l.TaskID, &l.CreatedAt,
				); scanErr == nil {
					data.Logs = append(data.Logs, l)
				}
			}
		}
	}

	templ.Handler(pages.Logs(data)).ServeHTTP(w, r)
}
