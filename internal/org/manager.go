package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// DesignOrg produces a standard v1 org plan for the given company and existing humans.
func (m *OrgManager) DesignOrg(company domain.Company, humans []domain.Employee) (*OrgPlan, error) {
	// Determine if there's a human CEO or CTO.
	var humanCEOID, humanCTOID string
	for _, h := range humans {
		role := strings.ToLower(h.Role)
		switch {
		case strings.Contains(role, "ceo") || strings.Contains(role, "chief executive"):
			humanCEOID = h.ID
		case strings.Contains(role, "cto") || strings.Contains(role, "chief technology"):
			humanCTOID = h.ID
		}
	}

	roles := []PlannedRole{
		{
			ID:         "ceo-ae",
			Role:       "CEO",
			Department: "Executive",
			ReportsTo:  humanCEOID, // empty if no human CEO
			Rationale:  "Top-level AI executive responsible for company strategy and direction.",
		},
	}

	// Add CTO-AE only if there's no human CTO.
	if humanCTOID == "" {
		roles = append(roles, PlannedRole{
			ID:         "cto-ae",
			Role:       "CTO",
			Department: "Engineering",
			ReportsTo:  "ceo-ae",
			Rationale:  "AI technology leader responsible for engineering strategy.",
		})
	}

	ctoReportsTo := func() string {
		if humanCTOID != "" {
			return humanCTOID
		}
		return "cto-ae"
	}()

	roles = append(roles,
		PlannedRole{
			ID:         "product-ae",
			Role:       "Product Manager",
			Department: "Product",
			ReportsTo:  "ceo-ae",
			Rationale:  "AI product manager responsible for roadmap and prioritization.",
		},
		PlannedRole{
			ID:         "engineer-ae",
			Role:       "Software Engineer",
			Department: "Engineering",
			ReportsTo:  ctoReportsTo,
			Rationale:  "AI engineer responsible for implementation and delivery.",
		},
		PlannedRole{
			ID:         "developer-ae",
			Role:       "Developer",
			Department: "Engineering",
			ReportsTo:  ctoReportsTo,
			Rationale:  "AI developer responsible for writing, testing, and shipping code.",
		},
		PlannedRole{
			ID:         "sdr-ae",
			Role:       "SDR",
			Department: "Sales",
			ReportsTo:  "ceo-ae",
			Rationale:  "AI sales development representative for lead discovery and outbound outreach.",
		},
		PlannedRole{
			ID:         "cmo-ae",
			Role:       "CMO",
			Department: "Marketing",
			ReportsTo:  "ceo-ae",
			Rationale:  "AI Chief Marketing Officer responsible for brand, campaigns, and growth.",
		},
		PlannedRole{
			ID:         "designer-ae",
			Role:       "Designer",
			Department: "Design",
			ReportsTo:  "ceo-ae",
			Rationale:  "AI designer responsible for UI/UX design, visual assets, and brand consistency.",
		},
	)

	// Build hierarchy from roles.
	hierarchy := make([]ReportingLine, 0, len(roles))
	for _, r := range roles {
		hierarchy = append(hierarchy, ReportingLine{
			EmployeeID: r.ID,
			ReportsTo:  r.ReportsTo,
		})
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
	cfg.Runtime.HeartbeatSeconds = 300
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
