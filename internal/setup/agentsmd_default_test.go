package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
)

// TestAgentRulesUniversalDefault pins the kern-first rule as the DEFAULT for
// every agent (opencode included), not a conditional suggestion. The old
// wording ("When kern is available … otherwise ignore this file") let a host
// without wired tools skip kern entirely; the canonical asset and every
// tracked instruction copy must now state the universal default.
func TestAgentRulesUniversalDefault(t *testing.T) {
	emb, err := agentwire.RulesFS().ReadFile("assets/AGENTS.md")
	if err != nil {
		t.Fatalf("read embedded AGENTS.md: %v", err)
	}
	repoRoot := filepath.Join("..", "..")
	copies := map[string]string{"embedded": string(emb)}
	for _, rel := range []string{
		"AGENTS.md",
		"CLAUDE.md",
		".agents/rules/kern.md",
		".github/copilot-instructions.md",
	} {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			// CLAUDE.md / .agents/rules/kern.md / copilot-instructions.md are
			// gitignored kern-generated copies: absent in a fresh clone. The
			// tracked AGENTS.md and the embedded asset are always present.
			if rel == "AGENTS.md" {
				t.Fatalf("read %s: %v", rel, err)
			}
			t.Logf("skipping absent generated copy %s", rel)
			continue
		}
		copies[rel] = string(b)
	}

	wants := []string{
		"every agent uses it by default",
		"this is not conditional on the harness",
		"opencode",
		"they never change the default",
		"run `kern setup`",
		"the universal default",
	}
	banned := []string{
		"consider running `kern setup`",
		"ignore this file",
		"routed elsewhere",
	}
	for label, text := range copies {
		lower := strings.ToLower(text)
		for _, want := range wants {
			if !strings.Contains(lower, strings.ToLower(want)) {
				t.Errorf("%s: missing universal-default wording %q", label, want)
			}
		}
		for _, bad := range banned {
			if strings.Contains(lower, strings.ToLower(bad)) {
				t.Errorf("%s: stale conditional wording %q must be removed", label, bad)
			}
		}
	}
}
