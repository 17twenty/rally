package memory

import (
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/domain"
)

const maxContextChars = 2000

// BuildMemoryContext formats memory events into a context string for LLM prompts.
// Events are grouped by type (heuristics first, then reflections, then episodic)
// and ordered most recent first within each group. Output is truncated to ~2000 chars.
func BuildMemoryContext(memories []domain.MemoryEvent) string {
	if len(memories) == 0 {
		return ""
	}

	groups := map[string][]domain.MemoryEvent{
		"heuristic":  {},
		"reflection": {},
		"episodic":   {},
	}
	order := []string{"heuristic", "reflection", "episodic"}

	for _, m := range memories {
		if _, ok := groups[m.Type]; ok {
			groups[m.Type] = append(groups[m.Type], m)
		}
	}

	var sb strings.Builder
	sb.WriteString("## Memory Context\n\n")

	for _, t := range order {
		events := groups[t]
		if len(events) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n", strings.Title(t))) //nolint:staticcheck
		for _, e := range events {
			line := fmt.Sprintf("- %s\n", e.Content)
			if sb.Len()+len(line) > maxContextChars {
				sb.WriteString("- [truncated]\n")
				return sb.String()
			}
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
