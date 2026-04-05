package handlers

import (
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/templates/pages"
)

// DashboardHandler handles the main dashboard page.
type DashboardHandler struct {
	DB *db.DB
}

func (h *DashboardHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// Show handles GET / — the main dashboard.
func (h *DashboardHandler) Show(w http.ResponseWriter, r *http.Request) {
	data := pages.DashboardData{}

	if h.DB != nil {
		ctx := r.Context()

		// If no companies exist, show the home/setup page instead.
		companyCount, _ := h.q().CountCompanies(ctx)
		if companyCount == 0 {
			templ.Handler(pages.Home(true)).ServeHTTP(w, r)
			return
		}

		// Quick stats
		aeCount, _ := h.q().CountAEs(ctx)
		data.TotalAEs = int(aeCount)

		taskCount, _ := h.q().CountTasks(ctx)
		data.TotalTasks = int(taskCount)

		kbCount, _ := h.q().CountActiveKBEntries(ctx)
		data.TotalKBEntries = int(kbCount)

		// Agent cards: AEs with last active time from tool_logs
		aeRows, err := h.q().ListAEsWithLastActive(ctx)
		if err == nil {
			for _, row := range aeRows {
				ac := pages.AgentCard{
					Employee: daoAERowToEmployee(row.ID, row.CompanyID, row.Name, row.Role,
						row.Specialties, row.Type, row.Status, row.SlackUserID, db.PgTime(row.CreatedAt)),
				}
				if t, ok := row.LastActive.(time.Time); ok {
					ac.LastActive = t
				}
				data.AgentCards = append(data.AgentCards, ac)
			}
		}

		// Recent activity: last 20 tool logs with employee name
		logRows, err := h.q().ListRecentToolLogsWithEmployee(ctx, 20)
		if err == nil {
			for _, row := range logRows {
				data.RecentLogs = append(data.RecentLogs, pages.ToolLogRow{
					ID:           row.ID,
					EmployeeID:   row.EmployeeID,
					EmployeeName: row.EmployeeName,
					Tool:         row.Tool,
					Action:       row.Action,
					Success:      row.Success,
					TraceID:      row.TraceID,
					TaskID:       row.TaskID,
					CreatedAt:    db.PgTime(row.CreatedAt),
				})
			}
		}

		// Team work: active work items across all AEs
		twRows, err := h.q().ListActiveTeamWorkItems(ctx)
		if err == nil {
			for _, row := range twRows {
				data.TeamWork = append(data.TeamWork, pages.TeamWorkItem{
					OwnerName: row.OwnerName,
					OwnerRole: row.OwnerRole,
					Title:     row.Title,
					Status:    row.Status,
					Priority:  row.Priority,
					UpdatedAt: db.PgTime(row.UpdatedAt),
				})
			}
		}
	}

	templ.Handler(pages.Dashboard(data)).ServeHTTP(w, r)
}

// daoAERowToEmployee maps flattened AE row fields to a domain.Employee.
func daoAERowToEmployee(id, companyID, name, role, specialties, typ, status, slackUserID string, createdAt time.Time) domain.Employee {
	return domain.Employee{
		ID:          id,
		CompanyID:   companyID,
		Name:        name,
		Role:        role,
		Specialties: specialties,
		Type:        typ,
		Status:      status,
		SlackUserID: slackUserID,
		CreatedAt:   createdAt,
	}
}
