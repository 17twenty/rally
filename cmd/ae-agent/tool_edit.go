package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileHashCache tracks the last-read hash of files to detect external changes.
// Edits are rejected if the file changed since the last Read (staleness guard).
var (
	fileHashes   = map[string]string{}
	fileHashesMu sync.Mutex
)

// recordFileHash stores the hash of a file's content after a Read.
func recordFileHash(path string, content []byte) {
	h := sha256.Sum256(content)
	fileHashesMu.Lock()
	fileHashes[path] = fmt.Sprintf("%x", h)
	fileHashesMu.Unlock()
}

// checkFileHash returns true if the file's current content matches the last
// recorded hash. Returns true (allowing edit) if the file was never read.
func checkFileHash(path string) (bool, error) {
	fileHashesMu.Lock()
	lastHash, tracked := fileHashes[path]
	fileHashesMu.Unlock()

	if !tracked {
		return true, nil // never read — allow edit
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	h := sha256.Sum256(data)
	currentHash := fmt.Sprintf("%x", h)
	return currentHash == lastHash, nil
}

// execEdit performs a string-replacement edit on a file.
// Params: file_path, old_string, new_string, replace_all (bool, default false)
// Fails if old_string is not found, or if it matches multiple locations (unless replace_all).
// Includes a staleness guard: rejects edits if the file changed since the last Read.
func (d *LocalToolDispatcher) execEdit(_ context.Context, params map[string]any) (map[string]any, error) {
	path, _ := params["file_path"].(string)
	if path == "" {
		return nil, fmt.Errorf("Edit: file_path is required")
	}
	oldStr, _ := params["old_string"].(string)
	if oldStr == "" {
		return nil, fmt.Errorf("Edit: old_string is required")
	}
	newStr, _ := params["new_string"].(string)
	replaceAll, _ := params["replace_all"].(bool)

	fullPath := filepath.Join(d.WorkspacePath, filepath.Clean(path))

	// Staleness guard: check file hasn't changed since last Read.
	fresh, err := checkFileHash(fullPath)
	if err != nil {
		return nil, fmt.Errorf("Edit: check staleness: %w", err)
	}
	if !fresh {
		return nil, fmt.Errorf("Edit: file %s has changed since last Read — read it again before editing", path)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("Edit: read file: %w", err)
	}
	content := string(data)

	count := strings.Count(content, oldStr)
	if count == 0 {
		return nil, fmt.Errorf("Edit: old_string not found in %s", path)
	}
	if count > 1 && !replaceAll {
		return nil, fmt.Errorf("Edit: old_string matches %d locations in %s — use replace_all or provide more context to make it unique", count, path)
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		newContent = strings.Replace(content, oldStr, newStr, 1)
	}

	if err := os.WriteFile(fullPath, []byte(newContent), 0o644); err != nil {
		return nil, fmt.Errorf("Edit: write file: %w", err)
	}

	// Update the hash cache with the new content.
	recordFileHash(fullPath, []byte(newContent))

	return map[string]any{
		"path":         path,
		"replacements": count,
	}, nil
}
