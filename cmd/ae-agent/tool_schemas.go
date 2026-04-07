package main

// ToolDefinition describes a tool the LLM can call.
// This mirrors llm.ToolDefinition but lives in the agent package to avoid
// importing the server's llm package into the agent binary.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// localToolDefs returns the tool definitions for tools that execute inside the
// AE container. These are sent to Rally's /api/ae/llm/chat endpoint as part
// of the tools array.
func localToolDefs() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "Bash",
			Description: "Execute a bash command in the workspace. Returns stdout and exit_code. Use for running programs, installing packages, git operations, and any shell task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute",
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Working directory (default: /workspace)",
					},
					"timeout_seconds": map[string]any{
						"type":        "number",
						"description": "Timeout in seconds (default: 120)",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "Read",
			Description: "Read a file from the workspace. Returns content with line numbers. Use this before editing a file to understand its contents.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path relative to /workspace",
					},
					"offset": map[string]any{
						"type":        "number",
						"description": "Line number to start reading from (1-based, default: 1)",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Maximum number of lines to return (default: 200)",
					},
				},
				"required": []string{"file_path"},
			},
		},
		{
			Name:        "Write",
			Description: "Create or overwrite a file in the workspace. Use Read first if modifying an existing file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path relative to /workspace",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The full file content to write",
					},
				},
				"required": []string{"file_path", "content"},
			},
		},
		{
			Name:        "Edit",
			Description: "Make a targeted edit to a file using string replacement. You MUST Read the file first. Fails if old_string is not found or matches multiple locations (use replace_all for that).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path relative to /workspace",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact string to find and replace",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The replacement string",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace all occurrences (default: false, fails if >1 match)",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
		{
			Name:        "Grep",
			Description: "Search for a regex pattern across files in the workspace. Returns matching lines with file paths and line numbers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regex pattern to search for",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search in (default: workspace root)",
					},
					"glob": map[string]any{
						"type":        "string",
						"description": "File pattern filter (e.g., *.go, *.md)",
					},
					"output_mode": map[string]any{
						"type":        "string",
						"description": "files_with_matches (paths only) or content (matching lines, default)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "Glob",
			Description: "Find files matching a glob pattern in the workspace. Supports ** for recursive matching. Results sorted by modification time (newest first).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern (e.g., **/*.go, *.md, src/**/*.ts)",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search in (default: workspace root)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "ListFiles",
			Description: "List files and directories in the workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory path relative to /workspace (default: root)",
					},
				},
			},
		},
		{
			Name:        "SlackSend",
			Description: "Send a message to a Slack channel. Always use this to communicate with the team.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel": map[string]any{
						"type":        "string",
						"description": "Channel name (e.g., #general)",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "Message text to send",
					},
				},
				"required": []string{"channel", "text"},
			},
		},
		// --- Work Tracking ---
		{
			Name:        "BacklogList",
			Description: "List your current work items (backlog). Shows what you're working on, what's pending, and what's blocked.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status: todo, in_progress, blocked, done, or all (default: active items only)",
					},
				},
			},
		},
		{
			Name:        "BacklogAdd",
			Description: "Add a work item to your backlog. Use this to break down tasks into steps and track your progress.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":          map[string]any{"type": "string", "description": "Short title for the work item"},
					"description":    map[string]any{"type": "string", "description": "Detailed description"},
					"priority":       map[string]any{"type": "string", "description": "low, medium, high, or critical (default: medium)"},
					"parent_id":      map[string]any{"type": "string", "description": "ID of parent work item (for sub-tasks)"},
					"source_task_id": map[string]any{"type": "string", "description": "ID of the task this work item came from"},
				},
				"required": []string{"title"},
			},
		},
		{
			Name:        "BacklogUpdate",
			Description: "Update a work item's status or add a note. Use this to mark items in_progress, done, or blocked as you work.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "Work item ID"},
					"status": map[string]any{"type": "string", "description": "New status: todo, in_progress, done, blocked, cancelled"},
					"note":   map[string]any{"type": "string", "description": "Progress note or reason for status change"},
				},
				"required": []string{"id"},
			},
		},
		// --- Collaboration ---
		{
			Name:        "Delegate",
			Description: "Delegate a task to another team member by their role (e.g., CTO, Designer). Creates a work item for them.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target_role": map[string]any{"type": "string", "description": "Role of the AE to delegate to (e.g., CTO, Designer)"},
					"title":       map[string]any{"type": "string", "description": "Task title"},
					"description": map[string]any{"type": "string", "description": "What needs to be done"},
					"context":     map[string]any{"type": "string", "description": "Background context for the delegate"},
					"priority":    map[string]any{"type": "string", "description": "low, medium, high, or critical"},
				},
				"required": []string{"target_role", "title"},
			},
		},
		{
			Name:        "Escalate",
			Description: "Escalate an issue to human team members. Use when you're blocked, unsure, or need human approval. Posts to Slack.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason":  map[string]any{"type": "string", "description": "Why you're escalating"},
					"context": map[string]any{"type": "string", "description": "Background context"},
					"urgency": map[string]any{"type": "string", "description": "low, medium, or high"},
				},
				"required": []string{"reason"},
			},
		},
		{
			Name:        "SendMessage",
			Description: "Send a direct message to another AE by role. Use for quick questions or updates, not for task delegation.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target_role": map[string]any{"type": "string", "description": "Role of the AE to message (e.g., CTO, Designer)"},
					"message":     map[string]any{"type": "string", "description": "Your message"},
				},
				"required": []string{"target_role", "message"},
			},
		},
		{
			Name:        "UpdateTask",
			Description: "Update the status of a task assigned to you. Use this to mark tasks as in_progress, done, or blocked.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID"},
					"status":  map[string]any{"type": "string", "description": "New status: in_progress, done, blocked"},
					"note":    map[string]any{"type": "string", "description": "Progress note"},
				},
				"required": []string{"task_id", "status"},
			},
		},
		// --- Memory ---
		{
			Name:        "Remember",
			Description: "Save something important to your long-term memory. Use this when you make a decision, learn something, or want to maintain consistency across cycles. What you remember will appear in future observations.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{
						"type":        "string",
						"description": "What to remember — be specific and factual",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "reflection (what you learned/decided), heuristic (a pattern/rule to follow), or episodic (what happened)",
					},
				},
				"required": []string{"content"},
			},
		},
		{
			Name:        "RecallMemory",
			Description: "Search your stored memories by keyword. Use this to recall past decisions, actions, or context from previous cycles.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Keyword or phrase to search for in your memories",
					},
				},
				"required": []string{"query"},
			},
		},
		// --- Credentials ---
		{
			Name:        "ListCredentials",
			Description: "List your available credentials (GitHub, Google, etc). Shows providers and status — no tokens exposed. Use this to check what integrations are configured before trying to use them.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "StoreCredential",
			Description: "Store a credential (API token, OAuth token, etc) securely in the vault. Use this when someone gives you a token via Slack or a message. The credential will be available to you in future cycles.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"provider":    map[string]any{"type": "string", "description": "Provider name (e.g. github, google, figma)"},
					"token":       map[string]any{"type": "string", "description": "The token or secret to store"},
					"access_type": map[string]any{"type": "string", "description": "Type: api, oauth, or browser_session (default: api)"},
					"scopes":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Permission scopes (e.g. repo, read:org)"},
				},
				"required": []string{"provider", "token"},
			},
		},
		// --- Hiring ---
		{
			Name:        "ProposeHire",
			Description: "CEO ONLY: Propose a new team member to be hired. Only the CEO can use this tool. If you are not the CEO, use SendMessage to ask the CEO to hire for you. The proposal will be reviewed by human leadership.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"role":       map[string]any{"type": "string", "description": "Job title (e.g., CTO, Head of Sales, Designer)"},
					"department": map[string]any{"type": "string", "description": "Department (e.g., Engineering, Sales, Marketing)"},
					"rationale":  map[string]any{"type": "string", "description": "Why this hire is needed — what gap does it fill?"},
					"reports_to": map[string]any{"type": "string", "description": "Role this person reports to (e.g., CEO, CTO)"},
					"channel":    map[string]any{"type": "string", "description": "Slack channel to post the approval link to"},
				},
				"required": []string{"role", "rationale"},
			},
		},
		{
			Name:        "ListTeam",
			Description: "List all current team members with their roles, types, and status. Use this to understand the current org before proposing hires.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		// --- Browser ---
		{
			Name:        "BrowserNavigate",
			Description: "Navigate to a URL and get the page title and text content.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "URL to navigate to",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}
