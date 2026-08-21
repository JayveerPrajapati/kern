package prprovider

import (
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		owner   string
		repo    string
	}{
		{
			name:  "ssh format",
			url:   "git@github.com:JayveerPrajapati/kern.git",
			owner: "JayveerPrajapati",
			repo:  "kern",
		},
		{
			name:  "https format",
			url:   "https://github.com/JayveerPrajapati/kern.git",
			owner: "JayveerPrajapati",
			repo:  "kern",
		},
		{
			name:  "https without git suffix",
			url:   "https://github.com/octocat/Hello-World",
			owner: "octocat",
			repo:  "Hello-World",
		},
		{
			name:    "non github url",
			url:     "git@gitlab.com:owner/repo.git",
			wantErr: true,
		},
		{
			name:    "empty url",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseRemoteURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRemoteURL(%q) expected error, got nil", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRemoteURL(%q) unexpected error: %v", tt.url, err)
			}
			if info.Owner != tt.owner {
				t.Errorf("owner = %q, want %q", info.Owner, tt.owner)
			}
			if info.Repo != tt.repo {
				t.Errorf("repo = %q, want %q", info.Repo, tt.repo)
			}
		})
	}
}

func TestNoopProviderCreatePR(t *testing.T) {
	np := NoopProvider{}
	res, err := np.CreatePR(Request{
		Owner: "JayveerPrajapati",
		Repo:  "kern",
		Title: "test",
		Head:  "feature-branch",
		Base:  "main",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("NoopProvider.CreatePR unexpected error: %v", err)
	}
	if res.Number != 0 {
		t.Errorf("Number = %d, want 0", res.Number)
	}
	if res.URL != "" {
		t.Errorf("URL = %q, want empty", res.URL)
	}
	if res.State != "noop" {
		t.Errorf("State = %q, want %q", res.State, "noop")
	}
}

func TestGitHubProviderCreatePRErrors(t *testing.T) {
	// Empty token (struct literal, not env) — the provider requires a token.
	emptyToken := &GitHubProvider{token: ""}
	_, err := emptyToken.CreatePR(Request{
		Owner: "octocat",
		Repo:  "Hello-World",
		Title: "test",
		Head:  "feature",
		Body:  "body",
	})
	if err == nil {
		t.Error("expected error when token is empty")
	}
	if !strings.Contains(err.Error(), "KERN_GITHUB_TOKEN") {
		t.Errorf("error %q should mention KERN_GITHUB_TOKEN", err)
	}

	// Empty owner/repo.
	withToken := &GitHubProvider{token: "fake-token"}
	_, err = withToken.CreatePR(Request{Repo: "Hello-World", Head: "feature"})
	if err == nil {
		t.Error("expected error when owner/repo empty")
	}

	// Empty head.
	_, err = withToken.CreatePR(Request{Owner: "octocat", Repo: "Hello-World"})
	if err == nil {
		t.Error("expected error when head empty")
	}
	if !strings.Contains(err.Error(), "head branch") {
		t.Errorf("error %q should mention head branch", err)
	}
}

func TestDetectRepoFailsOutsideGitRepo(t *testing.T) {
	// This directory is not a git repo, so DetectRepo should error — it must
	// not panic and must be handled by the caller gracefully.
	if _, err := DetectRepo("/nonexistent"); err == nil {
		t.Error("DetectRepo on nonexistent dir should error")
	}
}
