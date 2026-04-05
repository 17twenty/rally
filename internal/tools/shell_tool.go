package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// allowedCommands lists commands permitted in read-only mode.
var allowedCommands = map[string]bool{
	"cat":  true,
	"ls":   true,
	"grep": true,
	"find": true,
}

// blockedPatterns contains substrings that are never permitted.
var blockedPatterns = []string{"rm -rf", "sudo", "curl"}

// ShellTool executes shell commands in read-only mode with a 30s timeout.
type ShellTool struct{}

// Execute runs the "run" action: validates the command, then executes it.
func (t *ShellTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	if action != "run" {
		return nil, fmt.Errorf("shell: unknown action %q", action)
	}

	command, _ := input["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("shell.run: command required")
	}

	// Check blocked patterns (case-insensitive)
	lower := strings.ToLower(command)
	for _, blocked := range blockedPatterns {
		if strings.Contains(lower, blocked) {
			return nil, fmt.Errorf("shell.run: command contains blocked pattern %q", blocked)
		}
	}

	// Read-only mode: only allow cat, ls, grep, find
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("shell.run: empty command")
	}
	base := parts[0]
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if !allowedCommands[base] {
		return nil, fmt.Errorf("shell.run: %q not allowed (read-only mode permits: cat, ls, grep, find)", base)
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result := map[string]any{
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	}
	if runErr != nil {
		result["exit_error"] = runErr.Error()
		return result, fmt.Errorf("shell.run: %w", runErr)
	}
	return result, nil
}
