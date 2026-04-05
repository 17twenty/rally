package hiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/llm"
)

// GenerateAEName calls the LLM to generate a short human first name for the AE.
// Falls back to a role-based default if the LLM fails or returns an invalid result.
func GenerateAEName(ctx context.Context, router *llm.Router, role string, companyName string) (string, error) {
	systemPrompt := "You are a naming assistant. Respond with a single word only."
	userPrompt := fmt.Sprintf("Generate a single short human first name (one word only, no explanation) for an AI employee with the role of %s at %s. The name should feel approachable and professional. Respond with just the name.", role, companyName)

	result, err := router.Complete(ctx, router.DefaultModel(), systemPrompt, userPrompt, 10)
	if err == nil {
		name := strings.TrimSpace(result)
		if name != "" && !strings.Contains(name, " ") {
			return name, nil
		}
	}

	// Fallback names by role
	return fallbackName(role), nil
}

// fallbackName returns a default first name for the given role.
func fallbackName(role string) string {
	switch strings.ToLower(role) {
	case "ceo":
		return "Alex"
	case "cto":
		return "Jordan"
	case "software engineer", "engineer":
		return "Sam"
	case "developer":
		return "Casey"
	case "sdr":
		return "Riley"
	case "product manager":
		return "Morgan"
	case "cmo":
		return "Taylor"
	case "designer":
		return "Quinn"
	default:
		return "Drew"
	}
}

// GenerateSoulMD calls the LLM to generate a soul.md for the given AE role.
// Returns a markdown string covering personality, values, working style, and communication style.
func GenerateSoulMD(ctx context.Context, router *llm.Router, name string, role string, department string, reportsTo string, companyName string, mission string) (string, error) {
	systemPrompt := "You are an expert at designing AI employee personalities. Generate concise, authentic soul.md documents."
	userPrompt := fmt.Sprintf(`Generate a soul.md for %s, a %s artificial employee at %s.

Company Mission: %s
Department: %s
Reports To: %s

Include sections for: personality, values, working style, and communication style. Keep it focused and authentic.`,
		name, role, companyName, mission, department, reportsTo)

	result, err := router.Complete(ctx, router.DefaultModel(), systemPrompt, userPrompt, 1000)
	if err != nil {
		return "", fmt.Errorf("generate soul.md for %s: %w", role, err)
	}
	return result, nil
}
