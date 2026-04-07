package org

import (
	"testing"

	"github.com/17twenty/rally/internal/domain"
)

func TestDesignOrg_NoHumans(t *testing.T) {
	m := NewOrgManager()
	company := domain.Company{ID: "c1", Name: "Acme"}

	plan, err := m.DesignOrg(company, nil)
	if err != nil {
		t.Fatalf("DesignOrg error: %v", err)
	}

	// CEO-led hiring: only one role created initially.
	if len(plan.Roles) != 1 {
		t.Fatalf("expected 1 role (CEO only), got %d: %v", len(plan.Roles), roleIDs(plan.Roles))
	}

	if plan.Roles[0].ID != "ceo-ae" {
		t.Errorf("expected ceo-ae, got %s", plan.Roles[0].ID)
	}

	// No humans → CEO reports to nobody.
	if plan.Roles[0].ReportsTo != "" {
		t.Errorf("ceo-ae.ReportsTo = %q, want empty", plan.Roles[0].ReportsTo)
	}

	if len(plan.Hierarchy) != 1 {
		t.Errorf("hierarchy length %d, want 1", len(plan.Hierarchy))
	}
}

func TestDesignOrg_WithFounder(t *testing.T) {
	m := NewOrgManager()
	company := domain.Company{ID: "c2", Name: "Acme"}
	humans := []domain.Employee{
		{ID: "human-1", Role: "Founder"},
	}

	plan, err := m.DesignOrg(company, humans)
	if err != nil {
		t.Fatalf("DesignOrg error: %v", err)
	}

	if len(plan.Roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(plan.Roles))
	}

	// CEO should report to the founder.
	if plan.Roles[0].ReportsTo != "human-1" {
		t.Errorf("ceo-ae.ReportsTo = %q, want human-1", plan.Roles[0].ReportsTo)
	}
}

func TestGenerateEmployeeConfig_FullToolAccess(t *testing.T) {
	m := NewOrgManager()
	role := PlannedRole{ID: "ceo-ae", Role: "CEO", Department: "Executive"}
	plan := &OrgPlan{Roles: []PlannedRole{role}}

	cfg := m.GenerateEmployeeConfig(plan, role)

	if cfg.Runtime.HeartbeatSeconds != 60 {
		t.Errorf("heartbeat = %d, want 60", cfg.Runtime.HeartbeatSeconds)
	}

	// All AEs should have full tool access.
	expectedTools := []string{"slack", "github", "shell", "browser", "workspace", "google_workspace", "figma"}
	for _, tool := range expectedTools {
		if !cfg.Tools[tool] {
			t.Errorf("%s tool should be enabled", tool)
		}
	}
}

func roleIDs(roles []PlannedRole) []string {
	ids := make([]string, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	return ids
}
