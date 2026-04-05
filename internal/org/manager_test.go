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

	if len(plan.Roles) != 8 {
		t.Fatalf("expected 8 roles, got %d: %v", len(plan.Roles), roleIDs(plan.Roles))
	}

	// Build a lookup by ID.
	byID := make(map[string]PlannedRole, len(plan.Roles))
	for _, r := range plan.Roles {
		byID[r.ID] = r
	}

	// CEO-AE must exist and report to no one.
	ceo, ok := byID["ceo-ae"]
	if !ok {
		t.Fatal("missing ceo-ae")
	}
	if ceo.ReportsTo != "" {
		t.Errorf("ceo-ae.ReportsTo = %q, want empty", ceo.ReportsTo)
	}

	// CTO-AE must exist and report to CEO-AE.
	cto, ok := byID["cto-ae"]
	if !ok {
		t.Fatal("missing cto-ae")
	}
	if cto.ReportsTo != "ceo-ae" {
		t.Errorf("cto-ae.ReportsTo = %q, want ceo-ae", cto.ReportsTo)
	}

	// Product-AE must exist and report to CEO-AE.
	product, ok := byID["product-ae"]
	if !ok {
		t.Fatal("missing product-ae")
	}
	if product.ReportsTo != "ceo-ae" {
		t.Errorf("product-ae.ReportsTo = %q, want ceo-ae", product.ReportsTo)
	}

	// Engineer-AE must exist and report to CTO-AE.
	eng, ok := byID["engineer-ae"]
	if !ok {
		t.Fatal("missing engineer-ae")
	}
	if eng.ReportsTo != "cto-ae" {
		t.Errorf("engineer-ae.ReportsTo = %q, want cto-ae", eng.ReportsTo)
	}

	// Developer-AE must exist and report to CTO-AE.
	dev, ok := byID["developer-ae"]
	if !ok {
		t.Fatal("missing developer-ae")
	}
	if dev.ReportsTo != "cto-ae" {
		t.Errorf("developer-ae.ReportsTo = %q, want cto-ae", dev.ReportsTo)
	}

	// CMO-AE must exist and report to CEO-AE.
	cmo, ok := byID["cmo-ae"]
	if !ok {
		t.Fatal("missing cmo-ae")
	}
	if cmo.ReportsTo != "ceo-ae" {
		t.Errorf("cmo-ae.ReportsTo = %q, want ceo-ae", cmo.ReportsTo)
	}

	// Hierarchy must match roles length.
	if len(plan.Hierarchy) != len(plan.Roles) {
		t.Errorf("hierarchy length %d != roles length %d", len(plan.Hierarchy), len(plan.Roles))
	}
}

func TestDesignOrg_HumanCEO(t *testing.T) {
	m := NewOrgManager()
	company := domain.Company{ID: "c2", Name: "Acme"}
	humans := []domain.Employee{
		{ID: "human-1", Role: "CEO"},
	}

	plan, err := m.DesignOrg(company, humans)
	if err != nil {
		t.Fatalf("DesignOrg error: %v", err)
	}

	byID := make(map[string]PlannedRole, len(plan.Roles))
	for _, r := range plan.Roles {
		byID[r.ID] = r
	}

	ceo, ok := byID["ceo-ae"]
	if !ok {
		t.Fatal("missing ceo-ae")
	}
	if ceo.ReportsTo != "human-1" {
		t.Errorf("ceo-ae.ReportsTo = %q, want human-1", ceo.ReportsTo)
	}
}

func TestDesignOrg_HumanCTO(t *testing.T) {
	m := NewOrgManager()
	company := domain.Company{ID: "c3", Name: "Acme"}
	humans := []domain.Employee{
		{ID: "human-2", Role: "CTO"},
	}

	plan, err := m.DesignOrg(company, humans)
	if err != nil {
		t.Fatalf("DesignOrg error: %v", err)
	}

	byID := make(map[string]PlannedRole, len(plan.Roles))
	for _, r := range plan.Roles {
		byID[r.ID] = r
	}

	// CTO-AE should NOT exist.
	if _, ok := byID["cto-ae"]; ok {
		t.Error("cto-ae should not be created when human CTO exists")
	}

	// Engineer-AE should report to the human CTO.
	eng, ok := byID["engineer-ae"]
	if !ok {
		t.Fatal("missing engineer-ae")
	}
	if eng.ReportsTo != "human-2" {
		t.Errorf("engineer-ae.ReportsTo = %q, want human-2", eng.ReportsTo)
	}

	// Developer-AE should also report to the human CTO.
	dev, ok := byID["developer-ae"]
	if !ok {
		t.Fatal("missing developer-ae")
	}
	if dev.ReportsTo != "human-2" {
		t.Errorf("developer-ae.ReportsTo = %q, want human-2", dev.ReportsTo)
	}

	// Should have 7 AEs (no cto-ae: ceo, product, engineer, developer, sdr, cmo, designer).
	if len(plan.Roles) != 7 {
		t.Errorf("expected 7 roles when human CTO exists, got %d", len(plan.Roles))
	}
}

func TestGenerateEmployeeConfig_Defaults(t *testing.T) {
	m := NewOrgManager()
	role := PlannedRole{ID: "ceo-ae", Role: "CEO", Department: "Executive"}
	plan := &OrgPlan{Roles: []PlannedRole{role}}

	cfg := m.GenerateEmployeeConfig(plan, role)

	// DefaultModelRef is left empty here; the hiring flow sets it from the LLM router config.
	if cfg.Cognition.DefaultModelRef != "" {
		t.Errorf("model = %q, want empty (set by hiring flow)", cfg.Cognition.DefaultModelRef)
	}
	if cfg.Runtime.HeartbeatSeconds != 300 {
		t.Errorf("heartbeat = %d, want 300", cfg.Runtime.HeartbeatSeconds)
	}
	if !cfg.Tools["slack"] {
		t.Error("slack tool should be enabled")
	}
	if !cfg.Tools["github"] {
		t.Error("github tool should be enabled")
	}
}

func roleIDs(roles []PlannedRole) []string {
	ids := make([]string, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	return ids
}
