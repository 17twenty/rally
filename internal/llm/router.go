package llm

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/17twenty/rally/internal/domain"
	"gopkg.in/yaml.v3"
)

// Usage holds token consumption data from an LLM call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// CompletionResult holds the full result of an LLM call.
type CompletionResult struct {
	Text  string
	Usage Usage
}

// Message represents a single chat message.
type Message struct {
	Role    string
	Content string
}

// ProviderClient is the interface for LLM provider backends.
type ProviderClient interface {
	Complete(ctx context.Context, messages []Message, model string, maxTokens int) (CompletionResult, error)
}

// Router routes LLM completion requests to the appropriate model/provider.
type Router struct {
	providers       map[string]ProviderClient
	toolProviders   map[string]ToolUseProvider // tool-use capable wrappers per provider
	models          map[string]*domain.ModelConfig
	defaultModelRef string
}

// DefaultModel returns the configured default model ref from models.yaml.
func (r *Router) DefaultModel() string {
	if r.defaultModelRef != "" {
		return r.defaultModelRef
	}
	return "greenthread-gpt-oss-120b" // ultimate fallback
}

// NewRouter builds a Router from model and provider configs.
func NewRouter(models []*domain.ModelConfig, providers []*domain.ProviderConfig) *Router {
	r := &Router{
		providers:     make(map[string]ProviderClient),
		toolProviders: make(map[string]ToolUseProvider),
		models:        make(map[string]*domain.ModelConfig),
	}

	for _, p := range providers {
		if p.APIStyle == "" {
			log.Printf("llm: provider %q has no api_style configured, skipping", p.ID)
			continue
		}
		apiKey := os.Getenv(p.APIKeyEnv)
		if apiKey == "" {
			log.Printf("llm: provider %q has no API key set (env: %s), skipping", p.ID, p.APIKeyEnv)
			continue
		}
		switch p.APIStyle {
		case "anthropic":
			sdkClient := NewAnthropicSDKClient(apiKey)
			r.providers[p.ID] = sdkClient
			r.toolProviders[p.ID] = sdkClient // native tool-use
		case "openai":
			sdkClient := NewOpenAISDKClient(apiKey, p.BaseURL)
			r.providers[p.ID] = sdkClient
			r.toolProviders[p.ID] = sdkClient // native tool-use via streaming
		default:
			log.Printf("llm: provider %q has unknown api_style %q, skipping", p.ID, p.APIStyle)
		}
	}

	for _, m := range models {
		r.models[m.ID] = m
	}

	return r
}

// Complete sends a system+user prompt to the model identified by modelRef.
func (r *Router) Complete(ctx context.Context, modelRef string, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	m, ok := r.models[modelRef]
	if !ok {
		return "", fmt.Errorf("llm: unknown model %q", modelRef)
	}

	client, ok := r.providers[m.ProviderID]
	if !ok {
		return "", fmt.Errorf("llm: no provider client for %q", m.ProviderID)
	}

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	start := time.Now()
	result, err := client.Complete(ctx, messages, m.ModelName, maxTokens)
	duration := time.Since(start)

	if err != nil {
		slog.Warn("llm_request_failed",
			"model_ref", modelRef,
			"model_name", m.ModelName,
			"provider", m.ProviderID,
			"duration_ms", duration.Milliseconds(),
			"err", err,
		)
		return "", err
	}

	slog.Info("llm_request",
		"model_ref", modelRef,
		"model_name", m.ModelName,
		"provider", m.ProviderID,
		"system_len", len(systemPrompt),
		"user_len", len(userPrompt),
		"max_tokens", maxTokens,
		"input_tokens", result.Usage.InputTokens,
		"output_tokens", result.Usage.OutputTokens,
		"duration_ms", duration.Milliseconds(),
	)

	return result.Text, nil
}

// CompleteWithTools sends a multi-turn conversation with tool definitions to the
// model identified by modelRef. It automatically selects native tool-use or
// prompt-based fallback depending on the provider's capabilities.
func (r *Router) CompleteWithTools(
	ctx context.Context,
	modelRef string,
	messages []ConversationMessage,
	tools []ToolDefinition,
	maxTokens int,
) (ConversationResult, error) {
	m, ok := r.models[modelRef]
	if !ok {
		return ConversationResult{}, fmt.Errorf("llm: unknown model %q", modelRef)
	}

	tp := r.getToolProvider(m.ProviderID)
	if tp == nil {
		return ConversationResult{}, fmt.Errorf("llm: no provider client for %q", m.ProviderID)
	}

	start := time.Now()
	result, err := tp.CompleteWithTools(ctx, messages, tools, m.ModelName, maxTokens)
	duration := time.Since(start)

	if err != nil {
		slog.Warn("llm_chat_failed",
			"model_ref", modelRef,
			"model_name", m.ModelName,
			"provider", m.ProviderID,
			"duration_ms", duration.Milliseconds(),
			"err", err,
		)
		return ConversationResult{}, err
	}

	slog.Info("llm_chat",
		"model_ref", modelRef,
		"model_name", m.ModelName,
		"provider", m.ProviderID,
		"stop_reason", result.StopReason,
		"tool_calls", len(result.Message.ToolCalls),
		"input_tokens", result.Usage.InputTokens,
		"output_tokens", result.Usage.OutputTokens,
		"duration_ms", duration.Milliseconds(),
	)

	return result, nil
}

// ContextWindow returns the context window size for the given model ref.
func (r *Router) ContextWindow(modelRef string) int {
	m, ok := r.models[modelRef]
	if !ok || m.ContextWindow == 0 {
		return 128000 // safe default
	}
	return m.ContextWindow
}

// getToolProvider returns the ToolUseProvider for a given provider ID,
// creating a fallback wrapper if one doesn't exist yet.
func (r *Router) getToolProvider(providerID string) ToolUseProvider {
	if tp, ok := r.toolProviders[providerID]; ok {
		return tp
	}
	client, ok := r.providers[providerID]
	if !ok {
		return nil
	}
	// Wrap basic ProviderClient with fallback tool-use.
	// TODO: detect native tool-use capability and use it when available.
	tp := &FallbackToolUseProvider{Client: client}
	r.toolProviders[providerID] = tp
	return tp
}

// NewDefaultRouter loads config/models.yaml and config/providers.yaml and
// returns a configured Router. Returns a stub router if files are not found.
func NewDefaultRouter() *Router {
	modelsCfg, err := loadModelsYAML("config/models.yaml")
	if err != nil {
		log.Printf("llm: failed to load config/models.yaml: %v", err)
		return &Router{providers: make(map[string]ProviderClient), models: make(map[string]*domain.ModelConfig)}
	}

	providers, err := loadProvidersYAML("config/providers.yaml")
	if err != nil {
		log.Printf("llm: failed to load config/providers.yaml: %v", err)
		return &Router{providers: make(map[string]ProviderClient), models: make(map[string]*domain.ModelConfig)}
	}

	r := NewRouter(modelsCfg.Models, providers)
	r.defaultModelRef = modelsCfg.DefaultModel
	log.Printf("llm: loaded %d models, %d providers, default=%s", len(modelsCfg.Models), len(providers), r.DefaultModel())
	return r
}

type modelsConfig struct {
	DefaultModel string               `yaml:"default_model"`
	Models       []*domain.ModelConfig `yaml:"-"`
}

func loadModelsYAML(path string) (*modelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		DefaultModel string `yaml:"default_model"`
		Models       []struct {
			ID            string `yaml:"id"`
			Name          string `yaml:"name"`
			ModelName     string `yaml:"model_name"`
			ProviderID    string `yaml:"provider_id"`
			ContextWindow int    `yaml:"context_window"`
		} `yaml:"models"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &modelsConfig{DefaultModel: raw.DefaultModel}
	for _, m := range raw.Models {
		modelName := m.ModelName
		if modelName == "" {
			modelName = m.ID
		}
		cfg.Models = append(cfg.Models, &domain.ModelConfig{
			ID:            m.ID,
			Name:          m.Name,
			ModelName:     modelName,
			ProviderID:    m.ProviderID,
			ContextWindow: m.ContextWindow,
		})
	}
	return cfg, nil
}

func loadProvidersYAML(path string) ([]*domain.ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Providers []struct {
			ID        string `yaml:"id"`
			Name      string `yaml:"name"`
			APIStyle  string `yaml:"api_style"`
			BaseURL   string `yaml:"base_url"`
			APIKeyEnv string `yaml:"api_key_env"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	result := make([]*domain.ProviderConfig, 0, len(raw.Providers))
	for _, p := range raw.Providers {
		result = append(result, &domain.ProviderConfig{
			ID:        p.ID,
			Name:      p.Name,
			APIStyle:  p.APIStyle,
			BaseURL:   p.BaseURL,
			APIKeyEnv: p.APIKeyEnv,
		})
	}
	return result, nil
}
