package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// execGlob finds files matching a glob pattern in the workspace.
// Params: pattern (e.g., "**/*.go", "*.md"), path (dir, default /workspace)
// Returns matching file paths sorted by modification time (newest first).
func (d *LocalToolDispatcher) execGlob(ctx context.Context, params map[string]any) (map[string]any, error) {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("Glob: pattern is required")
	}

	searchPath, _ := params["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}
	rootDir := filepath.Join(d.WorkspacePath, filepath.Clean(searchPath))

	type fileEntry struct {
		path    string
		modTime int64
	}

	var matches []fileEntry

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path from root.
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			return nil
		}

		// Match the pattern against the relative path.
		// Support ** by checking each path segment.
		if matchGlob(pattern, rel) {
			info, infoErr := d.Info()
			modTime := int64(0)
			if infoErr == nil {
				modTime = info.ModTime().Unix()
			}
			matches = append(matches, fileEntry{path: rel, modTime: modTime})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Glob: walk: %w", err)
	}

	// Sort by modification time, newest first.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})

	// Cap results.
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, m.path)
		if len(paths) >= 200 {
			break
		}
	}

	return map[string]any{
		"files":   paths,
		"count":   len(paths),
		"pattern": pattern,
	}, nil
}

// matchGlob checks if a relative path matches a glob pattern.
// Supports *, ?, and ** (match across directory separators).
func matchGlob(pattern, path string) bool {
	// Handle ** patterns by converting to a simpler form.
	if strings.Contains(pattern, "**") {
		// Split pattern on **
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimRight(parts[0], "/")
			suffix := strings.TrimLeft(parts[1], "/")

			// Check if path has the right suffix.
			if suffix != "" {
				matched, _ := filepath.Match(suffix, filepath.Base(path))
				if !matched {
					return false
				}
			}
			// Check if path starts with prefix (or prefix is empty).
			if prefix != "" {
				return strings.HasPrefix(path, prefix+"/") || path == prefix
			}
			return true
		}
	}

	// Simple glob match (no **).
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	if matched {
		return true
	}
	// Also try matching the full relative path.
	matched, _ = filepath.Match(pattern, path)
	return matched
}
