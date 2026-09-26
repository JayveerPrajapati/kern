// Package prprovider provides provider-independent PR creation.
// The default NoopProvider preserves the prior behavior (render body, no network).
// The GitHubProvider creates real PRs via the GitHub REST API using net/http.
package prprovider

import "context"

// Request describes a PR to be created.
type Request struct {
	Owner string // repo owner (e.g. "JayveerPrajapati")
	Repo  string // repo name (e.g. "kern")
	Title string // PR title
	Head  string // source branch
	Base  string // target branch (default "main")
	Body  string // PR description (markdown)
}

// Result describes a created PR.
type Result struct {
	Number int    // PR number
	URL    string // web URL (e.g. https://github.com/owner/repo/pull/123)
	State  string // "open"
}

// CommentRequest describes a comment to post on an existing PR. It mirrors
// Request's owner/repo/body shape so the provider can build the endpoint URL
// without holding repo state.
type CommentRequest struct {
	Owner  string // repo owner (e.g. "JayveerPrajapati")
	Repo   string // repo name (e.g. "kern")
	Number int    // PR number to comment on
	Body   string // comment body (markdown)
}

// Provider creates pull requests and posts comments on a code-hosting platform.
type Provider interface {
	CreatePR(req Request) (*Result, error)
	CommentPR(ctx context.Context, req CommentRequest) error
}
