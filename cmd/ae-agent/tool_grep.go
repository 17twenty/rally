package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// execGrep performs regex search across workspace files.
// Params: pattern (regex), path (dir, default /workspace), glob (file filter),
//
//	output_mode ("files_with_matches" | "content", default "content")
//
// Uses ripgrep (rg) if available, falls back to grep -rn.
func (d *LocalToolDispatcher) execGrep(ctx context.Context, params map[string]any) (map[string]any, error) {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("Grep: pattern is required")
	}

	searchPath, _ := params["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}
	fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(searchPath))

	glob, _ := params["glob"].(string)
	outputMode, _ := params["output_mode"].(string)
	if outputMode == "" {
		outputMode = "content"
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Try ripgrep first, fall back to grep.
	var cmd *exec.Cmd
	if _, err := exec.LookPath("rg"); err == nil {
		args := []string{"-n", "--no-heading", "--color=never"}
		if outputMode == "files_with_matches" {
			args = append(args, "-l")
		}
		if glob != "" {
			args = append(args, "--glob", glob)
		}
		args = append(args, "--max-count=100", pattern, fullPath)
		cmd = exec.CommandContext(ctx, "rg", args...)
	} else {
		args := []string{"-rn"}
		if outputMode == "files_with_matches" {
			args = append(args, "-l")
		}
		if glob != "" {
			args = append(args, "--include="+glob)
		}
		args = append(args, pattern, fullPath)
		cmd = exec.CommandContext(ctx, "grep", args...)
	}

	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(output))

	// grep returns exit code 1 for "no matches" — not an error.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return map[string]any{
				"matches": 0,
				"content": "No matches found.",
			}, nil
		}
		return nil, fmt.Errorf("Grep: %w: %s", err, result)
	}

	// Strip the workspace path prefix from output for cleaner results.
	result = strings.ReplaceAll(result, d.WorkspacePath+"/", "")

	lines := strings.Split(result, "\n")
	if len(lines) > 200 {
		lines = lines[:200]
		result = strings.Join(lines, "\n") + "\n...[truncated at 200 lines]"
	}

	return map[string]any{
		"matches": len(lines),
		"content": result,
	}, nil
}
