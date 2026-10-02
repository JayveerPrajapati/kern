package mcpserve

import (
	"strings"
	"testing"
)

func TestCountRequestTokens(t *testing.T) {
	t.Parallel()
	empty := countRequestTokens("kern_foo", nil)
	if empty <= 0 {
		t.Errorf("countRequestTokens(name, nil) = %d, want > 0", empty)
	}
	fat := countRequestTokens("kern_foo", map[string]any{"text": strings.Repeat("word ", 2000)})
	if fat <= empty {
		t.Errorf("countRequestTokens with 2000 words = %d, want > bare-name count %d", fat, empty)
	}
}

func TestTokenMetadataFor(t *testing.T) {
	t.Parallel()
	args := map[string]any{"text": strings.Repeat("word ", 500)}
	meta := tokenMetadataFor("kern_x", args, "short output")
	if meta.TokensUsed <= 0 {
		t.Errorf("TokensUsed = %d, want > 0", meta.TokensUsed)
	}
	if meta.TokensReturned <= 0 {
		t.Errorf("TokensReturned = %d, want > 0", meta.TokensReturned)
	}
	if want := meta.TokensUsed - meta.TokensReturned; meta.Savings != want {
		t.Errorf("Savings = %d, want %d (used - returned)", meta.Savings, want)
	}
	if meta.Savings <= 0 {
		t.Errorf("Savings = %d, want > 0 for compressible call", meta.Savings)
	}
	if meta.EstimatedCost <= 0 {
		t.Errorf("EstimatedCost = %v, want > 0", meta.EstimatedCost)
	}
}

func TestTokenMetadataForClampsSavings(t *testing.T) {
	t.Parallel()
	// A response larger than the request (the common case: search/read tools
	// return more than they consume) must report zero savings, not negative.
	meta := tokenMetadataFor("kern_x", map[string]any{}, strings.Repeat("word ", 2000))
	if meta.Savings != 0 {
		t.Errorf("Savings = %d, want 0 when response exceeds request", meta.Savings)
	}
}
