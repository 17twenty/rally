package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
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

	c, err := h.q().GetCompany(ctx, id)
	if err != nil {
		http.Error(w, `{"error":"company not found"}`, http.StatusNotFound)
		return
	}
	company := domain.Company{
		ID: c.ID, Name: c.Name, Mission: db.Deref(c.Mission),
		Status: c.Status, CreatedAt: db.PgTime(c.CreatedAt),
	}

	if company.Status != "pending" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "company is not in pending state"})
		return
	}

	humanRows, err := h.q().ListHumanEmployeesByCompany(ctx, id)
	if err != nil {
		http.Error(w, `{"error":"failed to load employees"}`, http.StatusInternalServerError)
		return
	}
	var humans []domain.Employee
	for _, e := range humanRows {
		humans = append(humans, domain.Employee{
			ID: e.ID, Name: db.Deref(e.Name), Role: e.Role, Type: "human",
		})
	}

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
	_ = h.q().UpdateCompanyStatus(ctx, dao.UpdateCompanyStatusParams{ID: id, Status: "active"})

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

	c, err := h.q().GetCompany(ctx, id)
	if err != nil {
		http.Error(w, `{"error":"company not found"}`, http.StatusNotFound)
		return
	}
	companyStatus := c.Status

	empRows, err := h.q().ListEmployeesByCompany(ctx, id)
	if err != nil {
		http.Error(w, `{"error":"failed to load employees"}`, http.StatusInternalServerError)
		return
	}

	type empJSON struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Role   string `json:"role"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}

	var employees []empJSON
	var aeCount int
	for _, e := range empRows {
		employees = append(employees, empJSON{
			ID: e.ID, Name: db.Deref(e.Name), Role: e.Role, Type: e.Type, Status: e.Status,
		})
		if e.Type == "ae" {
			aeCount++
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
