package config

import (
	"os"
	"path/filepath"

	"github.com/17twenty/rally/internal/domain"
	"gopkg.in/yaml.v3"
)

// employeeConfigYAML mirrors the PRD §6.2 YAML format with snake_case keys.
type employeeConfigYAML struct {
	Employee struct {
		ID        string `yaml:"id"`
		Role      string `yaml:"role"`
		ReportsTo string `yaml:"reports_to"`
	} `yaml:"employee"`
	Identity struct {
		SoulFile string `yaml:"soul_file"`
	} `yaml:"identity"`
	Cognition struct {
		DefaultModelRef string            `yaml:"default_model_ref"`
		Routing         map[string]string `yaml:"routing"`
	} `yaml:"cognition"`
	Runtime struct {
		HeartbeatSeconds int `yaml:"heartbeat_seconds"`
	} `yaml:"runtime"`
	Tools map[string]bool `yaml:"tools"`
}

type modelRegistryYAML struct {
	Models []struct {
		ID            string `yaml:"id"`
		Name          string `yaml:"name"`
		ProviderID    string `yaml:"provider_id"`
		ContextWindow int    `yaml:"context_window"`
	} `yaml:"models"`
}

type providerRegistryYAML struct {
	Providers []struct {
		ID        string `yaml:"id"`
		Name      string `yaml:"name"`
		APIStyle  string `yaml:"api_style"`
		BaseURL   string `yaml:"base_url"`
		APIKeyEnv string `yaml:"api_key_env"`
	} `yaml:"providers"`
}

// LoadEmployeeConfig parses a single employee YAML config file.
func LoadEmployeeConfig(path string) (*domain.EmployeeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw employeeConfigYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &domain.EmployeeConfig{}
	cfg.ID = raw.Employee.ID
	cfg.Employee.ID = raw.Employee.ID
	cfg.Employee.Role = raw.Employee.Role
	cfg.Employee.ReportsTo = raw.Employee.ReportsTo
	cfg.Identity.SoulFile = raw.Identity.SoulFile
	cfg.Cognition.DefaultModelRef = raw.Cognition.DefaultModelRef
	cfg.Cognition.Routing = raw.Cognition.Routing
	cfg.Runtime.HeartbeatSeconds = raw.Runtime.HeartbeatSeconds
	cfg.Tools = raw.Tools
	return cfg, nil
}

// LoadEmployeeConfigsDir loads all .yaml files in a directory as employee configs.
func LoadEmployeeConfigsDir(dir string) ([]*domain.EmployeeConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var configs []*domain.EmployeeConfig
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		cfg, err := LoadEmployeeConfig(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// LoadModelConfig parses the model registry YAML and returns the first entry.
func LoadModelConfig(path string) (*domain.ModelConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw modelRegistryYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	if len(raw.Models) == 0 {
		return nil, nil
	}
	m := raw.Models[0]
	return &domain.ModelConfig{
		ID:            m.ID,
		Name:          m.Name,
		ProviderID:    m.ProviderID,
		ContextWindow: m.ContextWindow,
	}, nil
}

// LoadProviderConfig parses the provider registry YAML and returns the first entry.
func LoadProviderConfig(path string) (*domain.ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw providerRegistryYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	if len(raw.Providers) == 0 {
		return nil, nil
	}
	p := raw.Providers[0]
	return &domain.ProviderConfig{
		ID:        p.ID,
		Name:      p.Name,
		APIStyle:  p.APIStyle,
		BaseURL:   p.BaseURL,
		APIKeyEnv: p.APIKeyEnv,
	}, nil
}
