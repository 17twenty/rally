package handlers

import (
	"fmt"
	"net/http"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/templates/pages"
)

// TaskHandler handles task CRUD routes.
type TaskHandler struct {
	DB *db.DB
}

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
		compRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies ORDER BY name`)
		if err == nil {
			defer compRows.Close()
			for compRows.Next() {
				var c domain.Company
				if scanErr := compRows.Scan(&c.ID, &c.Name, &c.Mission, &c.Status, &c.CreatedAt); scanErr == nil {
					data.Companies = append(data.Companies, c)
				}
			}
		}

		// Build task query with optional filters.
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
	}

	templ.Handler(pages.Tasks(data)).ServeHTTP(w, r)
}

// New handles GET /tasks/new.
func (h *TaskHandler) New(w http.ResponseWriter, r *http.Request) {
	data := pages.TaskNewPageData{}

	if h.DB != nil {
		ctx := r.Context()

		compRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies ORDER BY name`)
		if err == nil {
			defer compRows.Close()
			for compRows.Next() {
				var c domain.Company
				if scanErr := compRows.Scan(&c.ID, &c.Name, &c.Mission, &c.Status, &c.CreatedAt); scanErr == nil {
					data.Companies = append(data.Companies, c)
				}
			}
		}

		empRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
			 FROM employees ORDER BY type, role`)
		if err == nil {
			defer empRows.Close()
			for empRows.Next() {
				var e domain.Employee
				if scanErr := empRows.Scan(
					&e.ID, &e.CompanyID, &e.Name, &e.Role, &e.Specialties,
					&e.Type, &e.Status, &e.SlackUserID, &e.CreatedAt,
				); scanErr == nil {
					data.Employees = append(data.Employees, e)
				}
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
		var assigneeArg *string
		if assigneeID != "" {
			assigneeArg = &assigneeID
		}
		_, err := h.DB.Pool.Exec(ctx,
			`INSERT INTO tasks (id, company_id, title, description, assignee_id, status, slack_channel)
			 VALUES ($1, $2, $3, $4, $5, 'open', $6)`,
			taskID, companyID, title, description, assigneeArg, slackChannel,
		)
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
	err := h.DB.Pool.QueryRow(ctx, `
		SELECT t.id, t.company_id, t.title, COALESCE(t.description,''),
		       COALESCE(t.assignee_id,''), t.status,
		       COALESCE(t.slack_thread_ts,''), COALESCE(t.slack_channel,''), t.created_at,
		       COALESCE(e.name, e.role, '') as assignee_name,
		       COALESCE(co.name, '') as company_name
		FROM tasks t
		LEFT JOIN employees e ON t.assignee_id = e.id
		LEFT JOIN companies co ON t.company_id = co.id
		WHERE t.id = $1`, id,
	).Scan(
		&data.Task.ID, &data.Task.CompanyID, &data.Task.Title, &data.Task.Description,
		&data.Task.AssigneeID, &data.Task.Status,
		&data.Task.SlackThreadTS, &data.Task.SlackChannel, &data.Task.CreatedAt,
		&data.AssigneeName, &data.CompanyName,
	)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

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
		_, err := h.DB.Pool.Exec(ctx,
			`UPDATE tasks SET status = $1 WHERE id = $2`, status, id)
		if err != nil {
			http.Error(w, "failed to update status", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/tasks/"+id, http.StatusSeeOther)
}
