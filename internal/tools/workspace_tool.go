package tools

import (
	"context"
	"fmt"

	"github.com/17twenty/rally/internal/workspace"
)

// WorkspaceTool provides AE access to the shared workspace artifact store.
type WorkspaceTool struct {
	WorkspaceStore *workspace.WorkspaceStore
}

// Execute dispatches a workspace action.
func (t *WorkspaceTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	switch action {
	case "read_file":
		fileID, _ := input["file_id"].(string)
		if fileID == "" {
			return nil, fmt.Errorf("workspace.read_file: file_id required")
		}
		f, err := t.WorkspaceStore.GetFile(ctx, fileID)
		if err != nil {
			return nil, fmt.Errorf("workspace.read_file: %w", err)
		}
		return map[string]any{
			"title":   f.Title,
			"content": f.Content,
			"path":    f.Path,
			"version": f.Version,
		}, nil

	case "write_file":
		companyID, _ := input["company_id"].(string)
		path, _ := input["path"].(string)
		title, _ := input["title"].(string)
		content, _ := input["content"].(string)
		createdBy, _ := input["created_by"].(string)
		mimeType, _ := input["mime_type"].(string)
		if companyID == "" || path == "" || content == "" {
			return nil, fmt.Errorf("workspace.write_file: company_id, path, and content required")
		}
		if mimeType == "" {
			mimeType = "text/plain"
		}
		fileID := newID()
		f := workspace.WorkspaceFile{
			ID:        fileID,
			CompanyID: companyID,
			Path:      path,
			Title:     title,
			Content:   content,
			MimeType:  mimeType,
			CreatedBy: createdBy,
		}
		if err := t.WorkspaceStore.SaveFile(ctx, f); err != nil {
			return nil, fmt.Errorf("workspace.write_file: %w", err)
		}
		// Fetch the saved file to return the actual version.
		saved, err := t.WorkspaceStore.GetFile(ctx, fileID)
		version := 1
		if err == nil {
			version = saved.Version
		}
		return map[string]any{"id": fileID, "version": version}, nil

	case "list_files":
		companyID, _ := input["company_id"].(string)
		pathPrefix, _ := input["path_prefix"].(string)
		if companyID == "" {
			return nil, fmt.Errorf("workspace.list_files: company_id required")
		}
		files, err := t.WorkspaceStore.ListFiles(ctx, companyID, pathPrefix)
		if err != nil {
			return nil, fmt.Errorf("workspace.list_files: %w", err)
		}
		result := make([]map[string]any, len(files))
		for i, f := range files {
			result[i] = map[string]any{
				"id":      f.ID,
				"path":    f.Path,
				"title":   f.Title,
				"status":  f.Status,
				"version": f.Version,
			}
		}
		return map[string]any{"files": result}, nil

	case "search_files":
		companyID, _ := input["company_id"].(string)
		query, _ := input["query"].(string)
		if companyID == "" || query == "" {
			return nil, fmt.Errorf("workspace.search_files: company_id and query required")
		}
		files, err := t.WorkspaceStore.SearchFiles(ctx, companyID, query)
		if err != nil {
			return nil, fmt.Errorf("workspace.search_files: %w", err)
		}
		result := make([]map[string]any, len(files))
		for i, f := range files {
			result[i] = map[string]any{
				"id":      f.ID,
				"path":    f.Path,
				"title":   f.Title,
				"status":  f.Status,
				"version": f.Version,
			}
		}
		return map[string]any{"files": result}, nil

	case "approve_file":
		fileID, _ := input["file_id"].(string)
		approvedBy, _ := input["approved_by"].(string)
		if fileID == "" || approvedBy == "" {
			return nil, fmt.Errorf("workspace.approve_file: file_id and approved_by required")
		}
		if err := t.WorkspaceStore.ApproveFile(ctx, fileID, approvedBy); err != nil {
			return nil, fmt.Errorf("workspace.approve_file: %w", err)
		}
		return map[string]any{"ok": true}, nil

	default:
		return nil, fmt.Errorf("workspace: unknown action %q", action)
	}
}
