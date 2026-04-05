package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalToolDispatcher handles tools that execute inside the AE container.
type LocalToolDispatcher struct {
	WorkspacePath string // /workspace
	ScratchPath   string // /home/ae/scratch
}

// Execute runs a local tool action and returns the result.
func (d *LocalToolDispatcher) Execute(ctx context.Context, tool, action string, params map[string]any) (map[string]any, error) {
	switch tool {
	case "shell":
		return d.execShell(ctx, params)
	case "workspace":
		return d.execWorkspace(ctx, action, params)
	case "browser":
		return d.execBrowser(ctx, action, params)
	default:
		return nil, fmt.Errorf("unknown local tool: %s", tool)
	}
}

// IsLocal returns true if the tool should be executed locally inside the container.
func IsLocal(tool string) bool {
	switch tool {
	case "shell", "workspace", "browser",
		"Bash", "Read", "Write", "Edit", "ListFiles", "Grep", "Glob",
		"BrowserNavigate", "BrowserScreenshot":
		return true
	default:
		return false
	}
}

func (d *LocalToolDispatcher) execShell(ctx context.Context, params map[string]any) (map[string]any, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("shell: command is required")
	}

	workDir, _ := params["working_dir"].(string)
	if workDir == "" {
		workDir = d.WorkspacePath
	}

	timeout := 120 * time.Second
	if t, ok := params["timeout_seconds"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "WORKSPACE="+d.WorkspacePath)

	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("shell exec: %w", err)
		}
	}

	slog.Info("shell_exec", "command", truncate(command, 100), "exit_code", exitCode)

	return map[string]any{
		"stdout":    string(output),
		"exit_code": exitCode,
	}, nil
}

// execRead reads a file with line numbers, supporting offset and limit.
func (d *LocalToolDispatcher) execRead(ctx context.Context, params map[string]any) (map[string]any, error) {
	path, _ := params["file_path"].(string)
	if path == "" {
		return nil, fmt.Errorf("Read: file_path is required")
	}
	fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(path))

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("Read: %w", err)
	}

	// Record hash for staleness guard (used by Edit tool).
	recordFileHash(fullPath, data)

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	offset := 1
	if o, ok := params["offset"].(float64); ok && o > 0 {
		offset = int(o)
	}
	limit := 200
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// Convert to 0-based index.
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		sb.WriteString(fmt.Sprintf("%6d\t%s\n", i+1, lines[i]))
	}

	return map[string]any{
		"content":     sb.String(),
		"path":        path,
		"total_lines": totalLines,
		"from_line":   offset,
		"to_line":     end,
	}, nil
}

// execWrite creates or overwrites a file.
func (d *LocalToolDispatcher) execWrite(ctx context.Context, params map[string]any) (map[string]any, error) {
	path, _ := params["file_path"].(string)
	if path == "" {
		return nil, fmt.Errorf("Write: file_path is required")
	}
	content, _ := params["content"].(string)
	fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(path))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, fmt.Errorf("Write: mkdir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("Write: %w", err)
	}

	return map[string]any{
		"path":    path,
		"written": len(content),
	}, nil
}

func (d *LocalToolDispatcher) execWorkspace(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	switch action {
	case "read":
		path, _ := params["path"].(string)
		fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(path))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("workspace read: %w", err)
		}
		return map[string]any{"content": string(data), "path": path}, nil

	case "write":
		path, _ := params["path"].(string)
		content, _ := params["content"].(string)
		fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return nil, fmt.Errorf("workspace mkdir: %w", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("workspace write: %w", err)
		}
		return map[string]any{"path": path, "written": len(content)}, nil

	case "list":
		dir, _ := params["dir"].(string)
		if dir == "" {
			dir = "."
		}
		fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(dir))
		entries, err := os.ReadDir(fullPath)
		if err != nil {
			return nil, fmt.Errorf("workspace list: %w", err)
		}
		var files []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			files = append(files, name)
		}
		return map[string]any{"files": files, "dir": dir}, nil

	default:
		return nil, fmt.Errorf("workspace: unknown action %q", action)
	}
}

func (d *LocalToolDispatcher) execBrowser(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	url, _ := params["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("browser: url is required")
	}

	switch action {
	case "navigate", "screenshot":
		// Use Playwright CLI for simple navigation/screenshot
		screenshotPath := filepath.Join(d.ScratchPath, fmt.Sprintf("screenshot-%d.png", time.Now().UnixMilli()))

		script := fmt.Sprintf(`
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto('%s', { waitUntil: 'networkidle', timeout: 30000 });
  const title = await page.title();
  const content = await page.textContent('body');
  await page.screenshot({ path: '%s', fullPage: true });
  console.log(JSON.stringify({ title, content: content.substring(0, 5000), screenshot: '%s' }));
  await browser.close();
})();`, strings.ReplaceAll(url, "'", "\\'"), screenshotPath, screenshotPath)

		cmd := exec.CommandContext(ctx, "node", "-e", script)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return map[string]any{"error": string(output)}, fmt.Errorf("browser navigate: %w", err)
		}
		return map[string]any{"output": string(output), "screenshot": screenshotPath}, nil

	default:
		return nil, fmt.Errorf("browser: unknown action %q", action)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
