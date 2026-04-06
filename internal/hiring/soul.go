package hiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/llm"
)

// GenerateAEName picks a unique name from the pool, avoiding existing names.
// Falls back to LLM generation if the pool is exhausted.
func GenerateAEName(ctx context.Context, router *llm.Router, role string, companyName string, existingNames []string) (string, error) {
	usedLower := make(map[string]bool, len(existingNames))
	for _, n := range existingNames {
		usedLower[strings.ToLower(n)] = true
	}

	// Pick from pool first — deterministic, no LLM needed.
	for _, name := range namePool {
		if !usedLower[strings.ToLower(name)] {
			return name, nil
		}
	}

	// Pool exhausted — ask LLM.
	avoidList := strings.Join(existingNames, ", ")
	result, err := router.Complete(ctx, router.DefaultModel(),
		"You are a naming assistant. Respond with a single word only.",
		fmt.Sprintf("Generate a unique first name not in this list: %s. One word only.", avoidList), 10)
	if err == nil {
		name := strings.TrimSpace(result)
		if name != "" && !strings.Contains(name, " ") && !usedLower[strings.ToLower(name)] {
			return name, nil
		}
	}

	return fmt.Sprintf("AE%d", len(existingNames)+1), nil
}

// namePool is a Docker-style pool of names. Each name is used at most once per org.
var namePool = []string{
	"Alex", "Blake", "Casey", "Dana", "Eden", "Finn", "Gray", "Harper",
	"Indigo", "Jordan", "Kai", "Luna", "Morgan", "Nova", "Onyx", "Parker",
	"Quinn", "Riley", "Sage", "Taylor", "Uri", "Vale", "Wren", "Xander",
	"Yara", "Zoe", "Ash", "Brook", "Camden", "Devon",
}

// fallbackName picks the first unused name from the pool.
func fallbackName(role string) string {
	return namePool[0] // Will be overridden by pool selection in GenerateAEName
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
