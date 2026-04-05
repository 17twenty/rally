package agent

import (
	"fmt"
	"strings"

	"github.com/17twenty/rally/internal/domain"
)

// BuildSystemPrompt constructs the AE's base system prompt from its role,
// soul.md content, reporting lines, available tools, and optional company policy.
func BuildSystemPrompt(employee domain.Employee, config domain.EmployeeConfig, soulContent string, policyDoc string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are %s, an AI employee with the role of %s.\n\n", employee.Name, employee.Role))

	if soulContent != "" {
		sb.WriteString("## Your Character & Soul\n")
		sb.WriteString(soulContent)
		sb.WriteString("\n\n")
	}

	if config.Employee.ReportsTo != "" {
		sb.WriteString(fmt.Sprintf("## Reporting Structure\nYou report to: %s\n\n", config.Employee.ReportsTo))
	}

	if len(config.Tools) > 0 {
		sb.WriteString("## Available Tools\n")
		for tool, enabled := range config.Tools {
			if enabled {
				sb.WriteString(fmt.Sprintf("- %s\n", tool))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Behaviour Guidelines\n")
	sb.WriteString("- Be concise and action-oriented.\n")
	sb.WriteString("- Prioritise the most impactful next action.\n")
	sb.WriteString("- Communicate proactively via Slack when decisions or blockers arise.\n")

	if policyDoc != "" {
		sb.WriteString("\n\n## Company Policy\nYou MUST adhere to the following company policy in all actions:\n")
		sb.WriteString(policyDoc)
	}

	return sb.String()
}

// BuildObservationPrompt formats recent context for the plan step.
func BuildObservationPrompt(slackEvents []domain.SlackEvent, memories []domain.MemoryEvent, kbEntries []domain.KnowledgebaseEntry) string {
	var sb strings.Builder

	sb.WriteString("## Current Observations\n\n")

	sb.WriteString(fmt.Sprintf("### Recent Slack Events (%d)\n", len(slackEvents)))
	for _, e := range slackEvents {
		sb.WriteString(fmt.Sprintf("- [%s] type=%s channel=%s\n", e.CreatedAt.Format("15:04"), e.EventType, e.Channel))
	}
	if len(slackEvents) == 0 {
		sb.WriteString("- No unprocessed Slack events.\n")
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("### Recent Memories (%d)\n", len(memories)))
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Type, m.CreatedAt.Format("2006-01-02"), truncate(m.Content, 120)))
	}
	if len(memories) == 0 {
		sb.WriteString("- No recent memories.\n")
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("### Relevant Knowledge Base Entries (%d)\n", len(kbEntries)))
	for _, k := range kbEntries {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", k.Title, truncate(k.Content, 100)))
	}
	if len(kbEntries) == 0 {
		sb.WriteString("- No relevant KB entries.\n")
	}

	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
