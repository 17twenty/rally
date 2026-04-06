package handlers

import (
	"fmt"
	"net/http"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/templates/pages"
)

// TaskHandler handles task CRUD routes.
type TaskHandler struct {
	DB *db.DB
}

func (h *TaskHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// List handles GET /tasks.
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	filterStatus := r.URL.Query().Get("status")
	filterCompanyID := r.URL.Query().Get("company_id")

	data := pages.TasksPageData{
		FilterStatus:    filterStatus,
		FilterCompanyID: filterCompanyID,
	}

	if h.DB != nil {
		ctx := r.Context()

		// Load companies for filter dropdown.
		if companies, err := h.q().ListCompaniesByName(ctx); err == nil {
			for _, c := range companies {
				data.Companies = append(data.Companies, domain.Company{
					ID:        c.ID,
					Name:      c.Name,
					Mission:   db.Deref(c.Mission),
					Status:    c.Status,
					CreatedAt: db.PgTime(c.CreatedAt),
				})
			}
		}

		// Build task query with optional filters (dynamic WHERE — stays as raw SQL).
		query := `
			SELECT t.id, t.company_id, t.title, COALESCE(t.description,''),
			       COALESCE(t.assignee_id,''), t.status,
			       COALESCE(t.slack_thread_ts,''), COALESCE(t.slack_channel,''), t.created_at,
			       COALESCE(e.name, e.role, '') as assignee_name,
			       COALESCE(co.name, '') as company_name
			FROM tasks t
			LEFT JOIN employees e ON t.assignee_id = e.id
			LEFT JOIN companies co ON t.company_id = co.id
			WHERE 1=1`
		args := []any{}
		argIdx := 1

		if filterStatus != "" {
			query += fmt.Sprintf(" AND t.status = $%d", argIdx)
			args = append(args, filterStatus)
			argIdx++
		}
		if filterCompanyID != "" {
			query += fmt.Sprintf(" AND t.company_id = $%d", argIdx)
			args = append(args, filterCompanyID)
			argIdx++
		}
		_ = argIdx
		query += " ORDER BY t.created_at DESC"

		rows, err := h.DB.Pool.Query(ctx, query, args...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var tr pages.TaskRow
				if scanErr := rows.Scan(
					&tr.Task.ID, &tr.Task.CompanyID, &tr.Task.Title, &tr.Task.Description,
					&tr.Task.AssigneeID, &tr.Task.Status,
					&tr.Task.SlackThreadTS, &tr.Task.SlackChannel, &tr.Task.CreatedAt,
					&tr.AssigneeName, &tr.CompanyName,
				); scanErr == nil {
					data.Tasks = append(data.Tasks, tr)
				}
			}
		}

		// Load work items (AE backlog) for each company in the filter.
		wiQuery := `
			SELECT wi.id, COALESCE(e.name, e.role) as owner_name, e.role as owner_role,
			       wi.title, wi.status, wi.priority, wi.updated_at
			FROM work_items wi
			JOIN employees e ON e.id = wi.owner_id
			WHERE wi.status NOT IN ('cancelled')
		`
		wiArgs := []any{}
		wiArgIdx := 1
		if filterCompanyID != "" {
			wiQuery += fmt.Sprintf(" AND wi.company_id = $%d", wiArgIdx)
			wiArgs = append(wiArgs, filterCompanyID)
			wiArgIdx++
		}
		if filterStatus != "" {
			wiQuery += fmt.Sprintf(" AND wi.status = $%d", wiArgIdx)
			wiArgs = append(wiArgs, filterStatus)
			wiArgIdx++
		}
		_ = wiArgIdx
		wiQuery += " ORDER BY CASE wi.status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 WHEN 'todo' THEN 2 ELSE 3 END, wi.updated_at DESC LIMIT 50"

		wiRows, wiErr := h.DB.Pool.Query(ctx, wiQuery, wiArgs...)
		if wiErr == nil {
			defer wiRows.Close()
			for wiRows.Next() {
				var wi pages.WorkItemRow
				var updatedAt pgtype.Timestamptz
				if scanErr := wiRows.Scan(&wi.ID, &wi.OwnerName, &wi.OwnerRole, &wi.Title, &wi.Status, &wi.Priority, &updatedAt); scanErr == nil {
					wi.UpdatedAt = db.PgTime(updatedAt).Format("Jan 2, 15:04")
					data.WorkItems = append(data.WorkItems, wi)
				}
			}
		}
	}

	templ.Handler(pages.Tasks(data)).ServeHTTP(w, r)
}

// New handles GET /tasks/new.
func (h *TaskHandler) New(w http.ResponseWriter, r *http.Request) {
	data := pages.TaskNewPageData{}

	if h.DB != nil {
		ctx := r.Context()

		if companies, err := h.q().ListCompaniesByName(ctx); err == nil {
			for _, c := range companies {
				data.Companies = append(data.Companies, domain.Company{
					ID:        c.ID,
					Name:      c.Name,
					Mission:   db.Deref(c.Mission),
					Status:    c.Status,
					CreatedAt: db.PgTime(c.CreatedAt),
				})
			}
		}

		if employees, err := h.q().ListAllEmployees(ctx); err == nil {
			for _, e := range employees {
				data.Employees = append(data.Employees, domain.Employee{
					ID:          e.ID,
					CompanyID:   e.CompanyID,
					Name:        db.Deref(e.Name),
					Role:        e.Role,
					Specialties: db.Deref(e.Specialties),
					Type:        e.Type,
					Status:      e.Status,
					SlackUserID: db.Deref(e.SlackUserID),
					CreatedAt:   db.PgTime(e.CreatedAt),
				})
			}
		}
	}

	templ.Handler(pages.TaskNew(data)).ServeHTTP(w, r)
}

// Create handles POST /tasks.
func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	companyID := r.FormValue("company_id")

	if title == "" || companyID == "" {
		http.Error(w, "title and company_id are required", http.StatusBadRequest)
		return
	}

	description := r.FormValue("description")
	assigneeID := r.FormValue("assignee_id")
	slackChannel := r.FormValue("slack_channel")

	taskID := newID()

	if h.DB != nil {
		ctx := r.Context()
		_, err := h.q().CreateTask(ctx, dao.CreateTaskParams{
			ID:           taskID,
			CompanyID:    companyID,
			Title:        title,
			Description:  db.Ref(description),
			AssigneeID:   db.Ref(assigneeID),
			Status:       "open",
			SlackChannel: db.Ref(slackChannel),
		})
		if err != nil {
			http.Error(w, "failed to create task", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/tasks/"+taskID, http.StatusSeeOther)
}

// Show handles GET /tasks/{id}.
func (h *TaskHandler) Show(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	data := pages.TaskDetailData{}

	if h.DB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	row, err := h.q().GetTaskDetail(ctx, id)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	data.Task = domain.Task{
		ID:            row.ID,
		CompanyID:     row.CompanyID,
		Title:         row.Title,
		Description:   row.Description,
		AssigneeID:    row.AssigneeID,
		Status:        row.Status,
		SlackThreadTS: row.SlackThreadTs,
		SlackChannel:  row.SlackChannel,
		CreatedAt:     db.PgTime(row.CreatedAt),
	}
	data.AssigneeName = row.AssigneeName
	data.CompanyName = row.CompanyName

	templ.Handler(pages.TaskDetail(data)).ServeHTTP(w, r)
}

// UpdateStatus handles POST /tasks/{id}/status.
func (h *TaskHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	status := r.FormValue("status")
	if status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	if h.DB != nil {
		ctx := r.Context()
		err := h.q().UpdateTaskStatus(ctx, dao.UpdateTaskStatusParams{
			ID:     id,
			Status: status,
		})
		if err != nil {
			http.Error(w, "failed to update status", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/tasks/"+id, http.StatusSeeOther)
}
