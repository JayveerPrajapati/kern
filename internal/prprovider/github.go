package prprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// GitHubProvider creates real PRs via the GitHub REST API.
// It requires KERN_GITHUB_TOKEN to be set. It uses net/http only (no SDK).
type GitHubProvider struct {
	client  *http.Client
	token   string // from KERN_GITHUB_TOKEN
	baseURL string // default "https://api.github.com"; override for GitHub Enterprise
}

// NewGitHubProvider creates a GitHubProvider from the KERN_GITHUB_TOKEN env var.
// Returns nil if KERN_GITHUB_TOKEN is not set (caller should fall back to Noop).
func NewGitHubProvider() *GitHubProvider {
	token := os.Getenv("KERN_GITHUB_TOKEN")
	if token == "" {
		return nil
	}
	return &GitHubProvider{
		client:  &http.Client{Timeout: 30 * time.Second},
		token:   token,
		baseURL: "https://api.github.com",
	}
}

// WithBaseURL overrides the API base URL (for GitHub Enterprise).
func (g *GitHubProvider) WithBaseURL(url string) *GitHubProvider {
	g.baseURL = url
	return g
}

func (g *GitHubProvider) CreatePR(req Request) (*Result, error) {
	if g.token == "" {
		return nil, fmt.Errorf("prprovider: KERN_GITHUB_TOKEN not set")
	}
	if req.Owner == "" || req.Repo == "" {
		return nil, fmt.Errorf("prprovider: owner and repo are required")
	}
	if req.Head == "" {
		return nil, fmt.Errorf("prprovider: head branch is required")
	}
	base := req.Base
	if base == "" {
		base = "main"
	}

	body := map[string]string{
		"title": req.Title,
		"head":  req.Head,
		"base":  base,
		"body":  req.Body,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("prprovider: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls", g.baseURL, req.Owner, req.Repo)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("prprovider: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+g.token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("prprovider: API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		// Parse GitHub error response for better diagnostics.
		var errResp struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Message == "" {
			errResp.Message = resp.Status
		}
		return nil, fmt.Errorf("prprovider: GitHub API returned %s: %s", resp.Status, errResp.Message)
	}

	var prResp struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&prResp); err != nil {
		return nil, fmt.Errorf("prprovider: decode response: %w", err)
	}

	return &Result{
		Number: prResp.Number,
		URL:    prResp.HTMLURL,
		State:  prResp.State,
	}, nil
}

// CommentPR posts a comment on an existing pull request via the GitHub REST
// API: POST /repos/{owner}/{repo}/issues/{prNumber}/comments (issue comments
// are the PR-comment endpoint). It requires the same KERN_GITHUB_TOKEN auth as
// CreatePR and errors on non-2xx responses, truncating the API error body to
// 500 chars for diagnostics.
func (g *GitHubProvider) CommentPR(ctx context.Context, req CommentRequest) error {
	if g.token == "" {
		return fmt.Errorf("prprovider: KERN_GITHUB_TOKEN not set")
	}
	if req.Owner == "" || req.Repo == "" {
		return fmt.Errorf("prprovider: owner and repo are required")
	}
	if req.Number <= 0 {
		return fmt.Errorf("prprovider: PR number is required")
	}
	jsonBody, err := json.Marshal(map[string]string{"body": req.Body})
	if err != nil {
		return fmt.Errorf("prprovider: marshal comment: %w", err)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", g.baseURL, req.Owner, req.Repo, req.Number)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("prprovider: create comment request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+g.token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("prprovider: comment API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read the API error body, truncated to 500 chars for diagnostics.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 501))
		msg := truncateRunes(string(raw), 500)
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("prprovider: GitHub API returned %s: %s", resp.Status, msg)
	}
	return nil
}

// truncateRunes returns s truncated to n runes (no mid-UTF8 splits).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
