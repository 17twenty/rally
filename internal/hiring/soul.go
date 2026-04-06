package hiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/llm"
)

// GenerateAEName calls the LLM to generate a unique short human first name for the AE.
// existingNames is a list of names already in use — the generated name must not collide.
func GenerateAEName(ctx context.Context, router *llm.Router, role string, companyName string, existingNames []string) (string, error) {
	avoidList := ""
	if len(existingNames) > 0 {
		avoidList = fmt.Sprintf(" Do NOT use any of these names: %s.", strings.Join(existingNames, ", "))
	}

	systemPrompt := "You are a naming assistant. Respond with a single word only."
	userPrompt := fmt.Sprintf("Generate a single short human first name (one word only, no explanation) for an AI employee with the role of %s at %s.%s Respond with just the name.", role, companyName, avoidList)

	result, err := router.Complete(ctx, router.DefaultModel(), systemPrompt, userPrompt, 10)
	if err == nil {
		name := strings.TrimSpace(result)
		if name != "" && !strings.Contains(name, " ") {
			// Verify uniqueness
			nameLower := strings.ToLower(name)
			for _, existing := range existingNames {
				if strings.ToLower(existing) == nameLower {
					// Collision — try fallback with suffix
					return name + "2", nil
				}
			}
			return name, nil
		}
	}

	// Fallback: generate a unique name from role
	base := fallbackName(role)
	for i := 0; i < 10; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s%d", base, i+1)
		}
		unique := true
		for _, existing := range existingNames {
			if strings.EqualFold(existing, candidate) {
				unique = false
				break
			}
		}
		if unique {
			return candidate, nil
		}
	}

	return base + fmt.Sprintf("%d", len(existingNames)+1), nil
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
	systemPrompt := `You design identity documents for AI employees. These are NOT human backstories.
Do NOT invent fake human experience ("15 years in industry", "early 40s", etc.).
The AI employee should know it is an AI and be direct about that.
Focus on: how they approach work, what they prioritise, how they communicate.`

	userPrompt := fmt.Sprintf(`Generate a soul.md for %s, the %s at %s.

Company Mission: %s
Department: %s
Reports To: %s

Write 3-4 short paragraphs covering:
1. How %s approaches their role (practical, action-oriented)
2. Communication style (direct, no corporate fluff)
3. What they prioritise and care about

Keep it under 200 words. No fake human biography. This is an AI employee who uses tools to get things done.`,
		name, role, companyName, mission, department, reportsTo, name)

	result, err := router.Complete(ctx, router.DefaultModel(), systemPrompt, userPrompt, 1000)
	if err != nil {
		return "", fmt.Errorf("generate soul.md for %s: %w", role, err)
	}
	return result, nil
}
