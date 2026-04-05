package config

import (
	"os"

	"github.com/17twenty/rally/internal/domain"
	"gopkg.in/yaml.v3"
)

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
