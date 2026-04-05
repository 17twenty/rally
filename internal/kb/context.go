package kb

import (
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/domain"
)

const maxContextChars = 3000

// BuildKBContext formats KB entries as a context string for agent prompts.
// It includes title, content, and tags for each active entry, truncated to ~3000 chars.
func BuildKBContext(entries []domain.KnowledgebaseEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== Company Knowledge Base ===\n\n")

	for _, e := range entries {
		section := fmt.Sprintf("## %s\n", e.Title)
		if len(e.Tags) > 0 {
			section += fmt.Sprintf("Tags: %s\n", strings.Join(e.Tags, ", "))
		}
		section += fmt.Sprintf("%s\n\n", e.Content)

		if sb.Len()+len(section) > maxContextChars {
			remaining := maxContextChars - sb.Len()
			if remaining > 20 {
				sb.WriteString(section[:remaining])
				sb.WriteString("...\n")
			}
			break
		}
		sb.WriteString(section)
	}

	return sb.String()
}
