package config

import (
	"os"
	"strings"
)

// PolicyDoc holds the raw policy content and parsed DOs/DON'Ts.
type PolicyDoc struct {
	Raw   string
	DOs   []string
	DONTs []string
}

// LoadPolicyFile reads a markdown file and returns its contents.
func LoadPolicyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ParsePolicyDoc extracts lines under '## Do' and '## Don't' / '## Avoid' headings.
// Raw is always set to the full content.
func ParsePolicyDoc(content string) PolicyDoc {
	doc := PolicyDoc{Raw: content}

	type section int
	const (
		sectionNone section = iota
		sectionDo
		sectionDont
	)

	current := sectionNone
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.HasPrefix(lower, "## do") && !strings.HasPrefix(lower, "## don") {
			current = sectionDo
			continue
		}
		if strings.HasPrefix(lower, "## don") || strings.HasPrefix(lower, "## avoid") {
			current = sectionDont
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			current = sectionNone
			continue
		}

		if trimmed == "" {
			continue
		}

		entry := strings.TrimPrefix(trimmed, "- ")
		entry = strings.TrimPrefix(entry, "* ")
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		switch current {
		case sectionDo:
			doc.DOs = append(doc.DOs, entry)
		case sectionDont:
			doc.DONTs = append(doc.DONTs, entry)
		}
	}

	return doc
}
