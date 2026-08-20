package context

import (
	"testing"
)

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestAnalyzeIntent(t *testing.T) {
	i := analyzeIntent("Add multi-tenant caching to the user service")
	if !contains(i.Verbs, "add") {
		t.Errorf("expected verb 'add', got %v", i.Verbs)
	}
	if !contains(i.Categories, "feature") {
		t.Errorf("expected category 'feature', got %v", i.Categories)
	}
	if i.RawText != "Add multi-tenant caching to the user service" {
		t.Errorf("RawText = %q", i.RawText)
	}
}

func TestAnalyzeIntentBugfix(t *testing.T) {
	i := analyzeIntent("Fix the N+1 query in checkout")
	if !contains(i.Verbs, "fix") {
		t.Errorf("expected verb 'fix', got %v", i.Verbs)
	}
	if !contains(i.Categories, "bugfix") {
		t.Errorf("expected category 'bugfix', got %v", i.Categories)
	}
}

func TestAnalyzeIntentConfigAndDocs(t *testing.T) {
	i := analyzeIntent("Update config.yaml and the README for the new service")
	if !contains(i.Categories, "config") {
		t.Errorf("expected category 'config', got %v", i.Categories)
	}
	if !contains(i.Categories, "docs") {
		t.Errorf("expected category 'docs', got %v", i.Categories)
	}
}

func TestExtractTargets(t *testing.T) {
	targets := extractTargets("refactor the CheckoutService and fix internal/api/router.go")
	if !contains(targets, "internal/api/router.go") {
		t.Errorf("expected file path target, got %v", targets)
	}
	if !contains(targets, "CheckoutService") {
		t.Errorf("expected symbol target, got %v", targets)
	}
}
