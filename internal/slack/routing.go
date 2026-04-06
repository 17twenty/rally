package slack

import (
	"regexp"
	"strings"

	"github.com/17twenty/rally/internal/domain"
)

var mentionRe = regexp.MustCompile(`@([A-Za-z]+-AE)`)

// ParseMentions extracts all @Name-AE patterns from message text and returns
// the role prefix (e.g., "Engineer", "CTO") without the "-AE" suffix.
func ParseMentions(text string) []string {
	matches := mentionRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var roles []string
	for _, m := range matches {
		// m[1] is the capture group, e.g., "Engineer-AE"
		role := strings.TrimSuffix(m[1], "-AE")
		key := strings.ToLower(role)
		if !seen[key] {
			seen[key] = true
			roles = append(roles, role)
		}
	}
	return roles
}

// MatchAEsByRole filters employees whose Role matches any of the given role
// prefixes (case-insensitive). Role prefixes are the part before "-AE".
func MatchAEsByRole(employees []domain.Employee, roles []string) []domain.Employee {
	if len(roles) == 0 {
		return nil
	}
	lower := make([]string, len(roles))
	for i, r := range roles {
		lower[i] = strings.ToLower(r)
	}
	var matched []domain.Employee
	for _, emp := range employees {
		empRole := strings.ToLower(strings.TrimSuffix(emp.Role, "-AE"))
		for _, r := range lower {
			if empRole == r {
				matched = append(matched, emp)
				break
			}
		}
	}
	return matched
}

// MatchAEsByName scans message text for any AE's first name OR role (case-insensitive).
// This handles "Hey Drew" and "Hey Go Engineer" style messages.
func MatchAEsByName(employees []domain.Employee, text string) []domain.Employee {
	lower := strings.ToLower(text)
	var matched []domain.Employee
	seen := make(map[string]bool)
	for _, emp := range employees {
		if emp.Type != "ae" {
			continue
		}
		// Check first name: "Drew (Go Developer)" → "Drew"
		name := emp.Name
		if idx := strings.Index(name, " ("); idx > 0 {
			name = name[:idx]
		}
		name = strings.Split(name, " ")[0]
		if name != "" && len(name) >= 3 && strings.Contains(lower, strings.ToLower(name)) && !seen[emp.ID] {
			seen[emp.ID] = true
			matched = append(matched, emp)
			continue
		}
		// Check role: "Go Engineer", "Chief Marketing Officer"
		if emp.Role != "" && len(emp.Role) >= 3 && strings.Contains(lower, strings.ToLower(emp.Role)) && !seen[emp.ID] {
			seen[emp.ID] = true
			matched = append(matched, emp)
		}
	}
	return matched
}

// ChannelToRoles maps a Slack channel name to the relevant AE role prefixes.
func ChannelToRoles(channel string) []string {
	switch strings.ToLower(channel) {
	case "engineering":
		return []string{"Engineer", "CTO"}
	case "product":
		return []string{"Product", "CEO"}
	case "general":
		return []string{"CEO"}
	case "exec":
		return []string{"CEO", "CTO"}
	default:
		return []string{"CEO"}
	}
}
