package config

import (
	"testing"
)

func TestLoadEmployeeConfig_CEOAE(t *testing.T) {
	cfg, err := LoadEmployeeConfig("../../config/employees/ceo-ae.yaml")
	if err != nil {
		t.Fatalf("LoadEmployeeConfig: %v", err)
	}
	if cfg.ID == "" {
		t.Error("expected non-empty ID")
	}
	if cfg.Employee.Role == "" {
		t.Error("expected non-empty Role")
	}
	if cfg.ID != "ceo-ae" {
		t.Errorf("expected ID %q, got %q", "ceo-ae", cfg.ID)
	}
	if cfg.Employee.Role != "CEO" {
		t.Errorf("expected Role %q, got %q", "CEO", cfg.Employee.Role)
	}
}
