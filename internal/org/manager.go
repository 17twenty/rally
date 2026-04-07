package org

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/17twenty/rally/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OrgManager designs org structure and generates AE definitions from company goals.
type OrgManager struct{}

// NewOrgManager creates a new OrgManager.
func NewOrgManager() *OrgManager {
	return &OrgManager{}
}

// PlannedRole represents a role to be created in the org.
type PlannedRole struct {
	ID         string
	Role       string
	Department string
	ReportsTo  string // empty = top of org, or employee ID / role ID
	Rationale  string
}

// ReportingLine represents a reporting relationship.
type ReportingLine struct {
	EmployeeID string
	ReportsTo  string
}

// OrgPlan is the result of DesignOrg.
type OrgPlan struct {
	Roles     []PlannedRole
	Hierarchy []ReportingLine
}

// DesignOrg creates a minimal org plan — just a CEO AE who reports to the
// human founder. The CEO then uses ProposeHire to build out the team based
// on the company's needs. No hardcoded roles beyond CEO.
func (m *OrgManager) DesignOrg(company domain.Company, humans []domain.Employee) (*OrgPlan, error) {
	// Find the human founder/CEO to set as reports-to.
	var founderID string
	for _, h := range humans {
		founderID = h.ID // use the first human as the reporting line
		break
	}

	roles := []PlannedRole{
		{
			ID:         "ceo-ae",
			Role:       "CEO",
			Department: "Executive",
			ReportsTo:  founderID,
			Rationale:  "Founding AI executive. Reviews company mission and proposes the team structure.",
		},
	}

	hierarchy := []ReportingLine{
		{EmployeeID: "ceo-ae", ReportsTo: founderID},
	}

	return &OrgPlan{
		Roles:     roles,
		Hierarchy: hierarchy,
	}, nil
}

// GenerateEmployeeConfig creates an EmployeeConfig with sensible defaults for a planned role.
func (m *OrgManager) GenerateEmployeeConfig(plan *OrgPlan, role PlannedRole) *domain.EmployeeConfig {
	cfg := &domain.EmployeeConfig{
		ID:         fmt.Sprintf("cfg-%s-%d", role.ID, time.Now().UnixNano()),
		EmployeeID: role.ID,
	}
	cfg.Employee.ID = role.ID
	cfg.Employee.Role = role.Role
	cfg.Employee.ReportsTo = role.ReportsTo
	// DefaultModelRef is set by the hiring flow from LLM router config
	cfg.Cognition.Routing = map[string]string{}
	cfg.Runtime.HeartbeatSeconds = 60 // overridden by HEARTBEAT_SECONDS env in hiring flow
	// All AEs get full tool access — they're treated as real employees.
	// Credentials (Google OAuth, Figma tokens) still need to be provisioned
	// separately, but tool access is unrestricted.
	cfg.Tools = map[string]bool{
		"slack":            true,
		"github":           true,
		"shell":            true,
		"browser":          true,
		"workspace":        true,
		"google_workspace": true,
		"figma":            true,
	}
	return cfg
}

// ApplyToDatabase inserts employees and org_structure rows for all planned roles.
func (m *OrgManager) ApplyToDatabase(ctx context.Context, db *pgxpool.Pool, companyID string, plan *OrgPlan) ([]domain.Employee, error) {
	// Map role ID → generated employee ID so we can resolve reporting lines.
	roleIDToEmployeeID := make(map[string]string, len(plan.Roles))
	employees := make([]domain.Employee, 0, len(plan.Roles))

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, role := range plan.Roles {
		employeeID := fmt.Sprintf("%s-%s-%d", companyID, role.ID, time.Now().UnixNano())
		roleIDToEmployeeID[role.ID] = employeeID

		_, err := tx.Exec(ctx, `
			INSERT INTO employees (id, company_id, name, role, type, status)
			VALUES ($1, $2, $3, $4, 'ae', 'active')
		`, employeeID, companyID, role.Role+"-AE", role.Role)
		if err != nil {
			return nil, fmt.Errorf("insert employee %s: %w", role.ID, err)
		}

		// Build and insert employee config.
		cfg := m.GenerateEmployeeConfig(plan, role)
		cfg.ID = fmt.Sprintf("cfg-%s", employeeID)
		cfg.EmployeeID = employeeID
		cfg.Employee.ID = employeeID

		cfgJSON, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("marshal config for %s: %w", role.ID, err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO employee_configs (id, employee_id, config)
			VALUES ($1, $2, $3)
		`, cfg.ID, employeeID, cfgJSON)
		if err != nil {
			return nil, fmt.Errorf("insert employee config %s: %w", role.ID, err)
		}

		employees = append(employees, domain.Employee{
			ID:        employeeID,
			CompanyID: companyID,
			Name:      role.Role + " AE",
			Role:      role.Role,
			Type:      "ae",
			Status:    "active",
			Config:    cfg,
		})
	}

	// Insert org_structure rows now that all employee IDs are known.
	for _, role := range plan.Roles {
		employeeID := roleIDToEmployeeID[role.ID]

		// Resolve ReportsTo: could be a role ID (in plan) or an external human ID.
		reportsTo := role.ReportsTo
		if mapped, ok := roleIDToEmployeeID[reportsTo]; ok {
			reportsTo = mapped
		}

		orgID := fmt.Sprintf("org-%s", employeeID)
		_, err := tx.Exec(ctx, `
			INSERT INTO org_structure (id, company_id, employee_id, reports_to, department)
			VALUES ($1, $2, $3, $4, $5)
		`, orgID, companyID, employeeID, nullableString(reportsTo), role.Department)
		if err != nil {
			return nil, fmt.Errorf("insert org_structure %s: %w", role.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return employees, nil
}

// nullableString returns nil if s is empty, otherwise &s. Used for nullable TEXT columns.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
