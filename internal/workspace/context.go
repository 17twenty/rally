package workspace

import (
	"fmt"
	"strings"
)

// BuildWorkspaceContext formats active workspace files as context for LLM prompts,
// truncated to approximately 3000 characters.
func BuildWorkspaceContext(files []WorkspaceFile) string {
	if len(files) == 0 {
		return ""
	}

	const maxLen = 3000
	var sb strings.Builder
	sb.WriteString("## Workspace Files\n\n")

	for _, f := range files {
		if f.Status != "active" {
			continue
		}
		section := fmt.Sprintf("### %s\nPath: %s | Version: %d\n%s\n\n", f.Title, f.Path, f.Version, f.Content)
		if sb.Len()+len(section) > maxLen {
			// Try to fit a truncated version.
			remaining := maxLen - sb.Len()
			if remaining > 80 {
				sb.WriteString(section[:remaining-3])
				sb.WriteString("...")
			}
			break
		}
		sb.WriteString(section)
	}

	return sb.String()
}
