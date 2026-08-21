// Package prprovider provides provider-independent PR creation.
// The default NoopProvider preserves the prior behavior (render body, no network).
// The GitHubProvider creates real PRs via the GitHub REST API using net/http.
package prprovider

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

// Provider creates pull requests on a code-hosting platform.
type Provider interface {
	CreatePR(req Request) (*Result, error)
}
