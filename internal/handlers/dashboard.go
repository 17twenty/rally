package handlers

import (
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/templates/pages"
)

// DashboardHandler handles the main dashboard page.
type DashboardHandler struct {
	DB *db.DB
}

// Show handles GET / — the main dashboard.
func (h *DashboardHandler) Show(w http.ResponseWriter, r *http.Request) {
	data := pages.DashboardData{}

	if h.DB != nil {
		ctx := r.Context()

		// If no companies exist, show the home/setup page instead.
		var companyCount int
		_ = h.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM companies`).Scan(&companyCount)
		if companyCount == 0 {
			templ.Handler(pages.Home(true)).ServeHTTP(w, r)
			return
		}

		// Quick stats
		_ = h.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM employees WHERE type = 'ae'`).Scan(&data.TotalAEs)
		_ = h.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&data.TotalTasks)
		_ = h.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM knowledgebase WHERE status = 'active'`).Scan(&data.TotalKBEntries)

		// Agent cards: AEs with last active time from tool_logs
		aeRows, err := h.DB.Pool.Query(ctx, `
			SELECT e.id, e.company_id, COALESCE(e.name,''), e.role, COALESCE(e.specialties,''),
			       e.type, e.status, COALESCE(e.slack_user_id,''), e.created_at,
			       MAX(tl.created_at) as last_active
			FROM employees e
			LEFT JOIN tool_logs tl ON tl.employee_id = e.id
			WHERE e.type = 'ae'
			GROUP BY e.id, e.company_id, e.name, e.role, e.specialties, e.type, e.status, e.slack_user_id, e.created_at
			ORDER BY e.created_at DESC
		`)
		if err == nil {
			defer aeRows.Close()
			for aeRows.Next() {
				var ac pages.AgentCard
				var lastActive *time.Time
				if scanErr := aeRows.Scan(
					&ac.Employee.ID, &ac.Employee.CompanyID, &ac.Employee.Name, &ac.Employee.Role,
					&ac.Employee.Specialties, &ac.Employee.Type, &ac.Employee.Status,
					&ac.Employee.SlackUserID, &ac.Employee.CreatedAt, &lastActive,
				); scanErr == nil {
					if lastActive != nil {
						ac.LastActive = *lastActive
					}
					data.AgentCards = append(data.AgentCards, ac)
				}
			}
		}

		// Recent activity: last 20 tool logs with employee name
		logRows, err := h.DB.Pool.Query(ctx, `
			SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id),
			       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,''), COALESCE(tl.task_id,''), tl.created_at
			FROM tool_logs tl
			LEFT JOIN employees e ON e.id = tl.employee_id
			ORDER BY tl.created_at DESC
			LIMIT 20
		`)
		if err == nil {
			defer logRows.Close()
			for logRows.Next() {
				var l pages.ToolLogRow
				if scanErr := logRows.Scan(
					&l.ID, &l.EmployeeID, &l.EmployeeName,
					&l.Tool, &l.Action, &l.Success, &l.TraceID, &l.TaskID, &l.CreatedAt,
				); scanErr == nil {
					data.RecentLogs = append(data.RecentLogs, l)
				}
			}
		}

		// Team work: active work items across all AEs
		twRows, err := h.DB.Pool.Query(ctx, `
			SELECT COALESCE(e.name, e.role), e.role, wi.title, wi.status, wi.priority, wi.updated_at
			FROM work_items wi
			JOIN employees e ON e.id = wi.owner_id
			WHERE wi.status IN ('in_progress', 'todo', 'blocked')
			ORDER BY CASE wi.status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END,
			         wi.updated_at DESC
			LIMIT 30
		`)
		if err == nil {
			defer twRows.Close()
			for twRows.Next() {
				var tw pages.TeamWorkItem
				if scanErr := twRows.Scan(&tw.OwnerName, &tw.OwnerRole, &tw.Title, &tw.Status, &tw.Priority, &tw.UpdatedAt); scanErr == nil {
					data.TeamWork = append(data.TeamWork, tw)
				}
			}
		}
	}

	templ.Handler(pages.Dashboard(data)).ServeHTTP(w, r)
}
