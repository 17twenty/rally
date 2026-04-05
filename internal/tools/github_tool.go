package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const githubAPIBase = "https://api.github.com"

// GitHubTool handles GitHub API actions.
type GitHubTool struct {
	ApprovalGranted bool   // required for write actions (create_comment)
	Token           string // OAuth/API token; if empty falls back to GITHUB_TOKEN env var
}

// Execute dispatches a GitHub action.
func (t *GitHubTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	token := t.Token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	switch action {
	case "list_prs":
		owner, _ := input["owner"].(string)
		repo, _ := input["repo"].(string)
		if owner == "" || repo == "" {
			return nil, fmt.Errorf("github.list_prs: owner and repo required")
		}
		url := fmt.Sprintf("%s/repos/%s/%s/pulls", githubAPIBase, owner, repo)
		return t.get(ctx, token, url)

	case "get_pr":
		owner, _ := input["owner"].(string)
		repo, _ := input["repo"].(string)
		number := input["number"]
		if owner == "" || repo == "" || number == nil {
			return nil, fmt.Errorf("github.get_pr: owner, repo, and number required")
		}
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%v", githubAPIBase, owner, repo, number)
		return t.get(ctx, token, url)

	case "create_comment":
		if !t.ApprovalGranted {
			return nil, fmt.Errorf("github.create_comment: requires human approval")
		}
		owner, _ := input["owner"].(string)
		repo, _ := input["repo"].(string)
		prNumber := input["pr_number"]
		body, _ := input["body"].(string)
		if owner == "" || repo == "" || prNumber == nil || body == "" {
			return nil, fmt.Errorf("github.create_comment: owner, repo, pr_number, and body required")
		}
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%v/comments", githubAPIBase, owner, repo, prNumber)
		return t.post(ctx, token, url, map[string]any{"body": body})

	default:
		return nil, fmt.Errorf("github: unknown action %q", action)
	}
}

func (t *GitHubTool) get(ctx context.Context, token, url string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: GET %s: status %d: %s", url, resp.StatusCode, data)
	}

	// Wrap raw response so callers always get a map
	return map[string]any{"data": json.RawMessage(data)}, nil
}

func (t *GitHubTool) post(ctx context.Context, token, url string, body map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("github: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: POST %s: status %d: %s", url, resp.StatusCode, data)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("github: parse response: %w", err)
	}
	return result, nil
}
