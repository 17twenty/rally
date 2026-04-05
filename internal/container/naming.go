package container

import (
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug converts a string to a lowercase, alphanumeric, dash-separated slug.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ContainerName returns the Docker container name for an AE.
// Format: rally-{company}-{role}-{name}
func ContainerName(companyName, role, aeName string) string {
	parts := []string{"rally", slug(companyName), slug(role), slug(aeName)}
	return strings.Join(parts, "-")
}
