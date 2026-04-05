package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/hiring"
	"github.com/17twenty/rally/internal/org"
	"github.com/17twenty/rally/internal/queue"
	"github.com/17twenty/rally/internal/slack"
)

// Build handles POST /companies/{id}/build
// Triggers org design + batch hiring synchronously (for development/testing).
// Only callable if company status is 'pending'.
func (h *CompanyHandler) Build(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, `{"error":"database not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	var company domain.Company
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies WHERE id = $1`, id,
	).Scan(&company.ID, &company.Name, &company.Mission, &company.Status, &company.CreatedAt)
	if err != nil {
		http.Error(w, `{"error":"company not found"}`, http.StatusNotFound)
		return
	}

	if company.Status != "pending" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "company is not in pending state"})
		return
	}

	humanRows, err := h.DB.Pool.Query(ctx,
		`SELECT id, COALESCE(name,''), role FROM employees WHERE company_id = $1 AND type = 'human'`, id)
	if err != nil {
		http.Error(w, `{"error":"failed to load employees"}`, http.StatusInternalServerError)
		return
	}
	var humans []domain.Employee
	for humanRows.Next() {
		var e domain.Employee
		if scanErr := humanRows.Scan(&e.ID, &e.Name, &e.Role); scanErr == nil {
			e.Type = "human"
			humans = append(humans, e)
		}
	}
	humanRows.Close()

	mgr := org.NewOrgManager()
	plan, err := mgr.DesignOrg(company, humans)
	if err != nil {
		http.Error(w, `{"error":"failed to design org"}`, http.StatusInternalServerError)
		return
	}

	var slackClient *slack.SlackClient
	if token := os.Getenv("SLACK_BOT_TOKEN"); token != "" {
		slackClient = slack.NewClient(token)
	}

	hirer := &hiring.Hirer{
		DB:               h.DB.Pool,
		LLMRouter:        h.LLMRouter,
		SlackClient:      slackClient,
		ContainerManager: h.ContainerManager,
		OnHired: func(ctx context.Context, employeeID, companyID string) {
			if queue.Client != nil {
				_, _ = queue.Client.Insert(ctx, queue.HeartbeatJobArgs{
					EmployeeID: employeeID,
					CompanyID:  companyID,
				}, nil)
			}
		},
	}

	employees, err := hirer.HireAll(ctx, id, plan, company)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
			"hired": len(employees),
			"total": len(plan.Roles),
		})
		return
	}

	// Update company status to 'active' after successful hiring
	_, _ = h.DB.Pool.Exec(ctx, `UPDATE companies SET status = 'active' WHERE id = $1`, id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ready",
		"hired":  len(employees),
		"total":  len(plan.Roles),
	})
}

// Status handles GET /companies/{id}/status
// Returns JSON: {employees: [...], status: 'building'|'ready', progress: 'N/M'}
func (h *CompanyHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, `{"error":"database not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	var companyStatus string
	err := h.DB.Pool.QueryRow(ctx, `SELECT status FROM companies WHERE id = $1`, id).Scan(&companyStatus)
	if err != nil {
		http.Error(w, `{"error":"company not found"}`, http.StatusNotFound)
		return
	}

	rows, err := h.DB.Pool.Query(ctx,
		`SELECT id, COALESCE(name,''), role, type, status FROM employees WHERE company_id = $1 ORDER BY created_at ASC`, id)
	if err != nil {
		http.Error(w, `{"error":"failed to load employees"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type empJSON struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Role   string `json:"role"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}

	var employees []empJSON
	var aeCount int
	for rows.Next() {
		var e empJSON
		if scanErr := rows.Scan(&e.ID, &e.Name, &e.Role, &e.Type, &e.Status); scanErr == nil {
			employees = append(employees, e)
			if e.Type == "ae" {
				aeCount++
			}
		}
	}

	status := "building"
	if companyStatus == "active" {
		status = "ready"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"employees": employees,
		"status":    status,
		"ae_count":  aeCount,
	})
}
