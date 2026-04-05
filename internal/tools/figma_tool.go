package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/vault"
)

// FigmaTool provides access to the Figma REST API for AEs.
type FigmaTool struct {
	Vault      *vault.CredentialVault
	EmployeeID string
}

func (t *FigmaTool) getToken(ctx context.Context, employeeID string) (string, error) {
	if t.Vault == nil {
		return "", fmt.Errorf("figma credentials not configured — add a Figma Personal Access Token via /credentials (provider: figma)")
	}
	token, err := t.Vault.Get(ctx, employeeID, "figma")
	if err != nil {
		return "", fmt.Errorf("figma credentials not configured — add a Figma Personal Access Token via /credentials (provider: figma)")
	}
	return token, nil
}

func (t *FigmaTool) doRequest(ctx context.Context, method, url, token string, body io.Reader) (map[string]any, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("figma: create request: %w", err)
	}
	req.Header.Set("X-Figma-Token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("figma: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("figma: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("figma: non-2xx response %d: %s", resp.StatusCode, string(respBytes))
	}

	var result map[string]any
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("figma: parse response: %w", err)
	}
	return result, nil
}

// Execute dispatches a Figma action.
func (t *FigmaTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	token, err := t.getToken(ctx, t.EmployeeID)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list_files":
		return t.listFiles(ctx, token, input)
	case "get_file":
		return t.getFile(ctx, token, input)
	case "list_components":
		return t.listComponents(ctx, token, input)
	case "export_assets":
		return t.exportAssets(ctx, token, input)
	case "get_comments":
		return t.getComments(ctx, token, input)
	case "post_comment":
		return t.postComment(ctx, token, input)
	default:
		return nil, fmt.Errorf("figma: unknown action %q", action)
	}
}

func (t *FigmaTool) listFiles(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	teamID, _ := input["team_id"].(string)

	var url string
	if teamID == "" {
		url = "https://api.figma.com/v1/me/files"
	} else {
		url = fmt.Sprintf("https://api.figma.com/v1/teams/%s/projects", teamID)
	}

	raw, err := t.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}

	// Normalize to {files: [{key, name, last_modified, thumbnail_url}]}
	var files []map[string]any
	if projects, ok := raw["projects"].([]any); ok {
		for _, p := range projects {
			if proj, ok := p.(map[string]any); ok {
				files = append(files, map[string]any{
					"key":           proj["id"],
					"name":          proj["name"],
					"last_modified": proj["last_modified"],
					"thumbnail_url": proj["thumbnail_url"],
				})
			}
		}
	} else if rawFiles, ok := raw["files"].([]any); ok {
		for _, f := range rawFiles {
			if file, ok := f.(map[string]any); ok {
				files = append(files, map[string]any{
					"key":           file["key"],
					"name":          file["name"],
					"last_modified": file["last_modified"],
					"thumbnail_url": file["thumbnail_url"],
				})
			}
		}
	}

	if files == nil {
		files = []map[string]any{}
	}
	return map[string]any{"files": files}, nil
}

func (t *FigmaTool) getFile(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	fileKey, _ := input["file_key"].(string)
	if fileKey == "" {
		return nil, fmt.Errorf("figma: get_file requires file_key")
	}

	url := fmt.Sprintf("https://api.figma.com/v1/files/%s", fileKey)
	raw, err := t.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"name":          raw["name"],
		"last_modified": raw["lastModified"],
	}

	// Extract components
	var components []map[string]any
	if compsRaw, ok := raw["components"].(map[string]any); ok {
		for id, c := range compsRaw {
			if comp, ok := c.(map[string]any); ok {
				components = append(components, map[string]any{
					"id":          id,
					"name":        comp["name"],
					"description": comp["description"],
				})
			}
		}
	}
	if components == nil {
		components = []map[string]any{}
	}
	result["components"] = components

	// Extract pages from document
	var pages []map[string]any
	if doc, ok := raw["document"].(map[string]any); ok {
		if children, ok := doc["children"].([]any); ok {
			for _, ch := range children {
				if page, ok := ch.(map[string]any); ok {
					pages = append(pages, map[string]any{
						"id":   page["id"],
						"name": page["name"],
					})
				}
			}
		}
	}
	if pages == nil {
		pages = []map[string]any{}
	}
	result["pages"] = pages

	return result, nil
}

func (t *FigmaTool) listComponents(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	fileKey, _ := input["file_key"].(string)
	if fileKey == "" {
		return nil, fmt.Errorf("figma: list_components requires file_key")
	}

	url := fmt.Sprintf("https://api.figma.com/v1/files/%s/components", fileKey)
	raw, err := t.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}

	var components []map[string]any
	if meta, ok := raw["meta"].(map[string]any); ok {
		if comps, ok := meta["components"].([]any); ok {
			for _, c := range comps {
				if comp, ok := c.(map[string]any); ok {
					components = append(components, map[string]any{
						"node_id":       comp["node_id"],
						"name":          comp["name"],
						"description":   comp["description"],
						"thumbnail_url": comp["thumbnail_url"],
					})
				}
			}
		}
	}
	if components == nil {
		components = []map[string]any{}
	}
	return map[string]any{"components": components}, nil
}

func (t *FigmaTool) exportAssets(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	fileKey, _ := input["file_key"].(string)
	if fileKey == "" {
		return nil, fmt.Errorf("figma: export_assets requires file_key")
	}

	nodeIDs, _ := input["node_ids"].(string)
	if nodeIDs == "" {
		return nil, fmt.Errorf("figma: export_assets requires node_ids")
	}

	format, _ := input["format"].(string)
	if format == "" {
		format = "png"
	}

	scale := 1.0
	if s, ok := input["scale"].(float64); ok && s > 0 {
		scale = s
	}

	url := fmt.Sprintf("https://api.figma.com/v1/images/%s?ids=%s&format=%s&scale=%g",
		fileKey, nodeIDs, format, scale)

	raw, err := t.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}

	images := map[string]any{}
	if imagesRaw, ok := raw["images"].(map[string]any); ok {
		images = imagesRaw
	}
	return map[string]any{"images": images}, nil
}

func (t *FigmaTool) getComments(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	fileKey, _ := input["file_key"].(string)
	if fileKey == "" {
		return nil, fmt.Errorf("figma: get_comments requires file_key")
	}

	url := fmt.Sprintf("https://api.figma.com/v1/files/%s/comments", fileKey)
	raw, err := t.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}

	var comments []map[string]any
	if commentsRaw, ok := raw["comments"].([]any); ok {
		for _, c := range commentsRaw {
			if comment, ok := c.(map[string]any); ok {
				authorName := ""
				if user, ok := comment["user"].(map[string]any); ok {
					authorName, _ = user["handle"].(string)
				}
				comments = append(comments, map[string]any{
					"id":         comment["id"],
					"message":    comment["message"],
					"author":     authorName,
					"created_at": comment["created_at"],
					"resolved":   comment["resolved_at"] != nil,
				})
			}
		}
	}
	if comments == nil {
		comments = []map[string]any{}
	}
	return map[string]any{"comments": comments}, nil
}

func (t *FigmaTool) postComment(ctx context.Context, token string, input map[string]any) (map[string]any, error) {
	fileKey, _ := input["file_key"].(string)
	if fileKey == "" {
		return nil, fmt.Errorf("figma: post_comment requires file_key")
	}
	message, _ := input["message"].(string)
	if message == "" {
		return nil, fmt.Errorf("figma: post_comment requires message")
	}

	bodyBytes, _ := json.Marshal(map[string]string{"message": message})
	url := fmt.Sprintf("https://api.figma.com/v1/files/%s/comments", fileKey)
	raw, err := t.doRequest(ctx, http.MethodPost, url, token, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}

	commentID, _ := raw["id"].(string)
	return map[string]any{"comment_id": commentID}, nil
}
