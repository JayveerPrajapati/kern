package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/prprovider"
)

// TestAutoPRProviderNoopWithoutToken pins the fallback: with no
// KERN_GITHUB_TOKEN the factory must return the no-op provider (the default
// that never touches the network), not fail.
func TestAutoPRProviderNoopWithoutToken(t *testing.T) {
	t.Setenv("KERN_GITHUB_TOKEN", "")
	if _, ok := AutoPRProvider().(prprovider.NoopProvider); !ok {
		t.Fatalf("AutoPRProvider() without token = %T, want prprovider.NoopProvider", AutoPRProvider())
	}
}

// TestAutoPRProviderGitHubWithToken pins the upgrade path: once a token is
// present the factory returns the real GitHub provider.
func TestAutoPRProviderGitHubWithToken(t *testing.T) {
	t.Setenv("KERN_GITHUB_TOKEN", "ghp_test_token")
	p := AutoPRProvider()
	if _, ok := p.(*prprovider.GitHubProvider); !ok {
		t.Fatalf("AutoPRProvider() with token = %T, want *prprovider.GitHubProvider", p)
	}
}
