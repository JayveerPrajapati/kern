package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
)

// redirectGlobalHome points the global rules resolution (HOME +
// XDG_CONFIG_HOME) at temp dirs so the check never touches the real home.
func redirectGlobalHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

// writeGlobalRulesFixture writes content at one of the six global rules paths
// (resolved under the redirected HOME/XDG), creating parent dirs.
func writeGlobalRulesFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// freshMarkedBlock is the canonical condensed rules block (what
// `kern setup --global-rules` writes today), stamped like a real install.
func freshMarkedBlock(t *testing.T) string {
	t.Helper()
	block, err := agentwire.CondenseGlobalRules()
	if err != nil {
		t.Fatal(err)
	}
	return agentwire.InsertManagedStamp(string(block))
}

// staleMarkedBlock carries the OLD rules shape (the 117-tool surface + the
// deleted kern_validate/kern_sandbox/kern_repair tools) inside the markers.
const staleMarkedBlock = "<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->\n" +
	"<!-- kern-version: dev -->\n" +
	"Instead of choosing among 117 individual `kern_*` tools, call **`kern_meta`** with a natural-language request; it classifies the request (deterministic keyword matching — no LLM, no network) and runs the right tool internally.\n" +
	"Every other tool is `KERN_MCP_FULL=1` only (`kern_validate`, `kern_sandbox`, `kern_repair`, …).\n" +
	"<!-- kern:global-rules end -->\n"

func TestCheckGlobalRulesFreshnessNoInstalledSlots(t *testing.T) {
	redirectGlobalHome(t)
	if got := checkGlobalRulesFreshness(); got != nil {
		t.Fatalf("no installed slots should produce no findings, got %+v", got)
	}
}

func TestCheckGlobalRulesFreshnessAllFresh(t *testing.T) {
	redirectGlobalHome(t)
	block := freshMarkedBlock(t)
	for _, p := range agentwire.GlobalRulesPaths() {
		content := block
		if strings.HasSuffix(p, ".mdc") {
			content = agentwire.EnsureMDCFrontmatter(content)
		}
		writeGlobalRulesFixture(t, p, content)
	}
	findings := checkGlobalRulesFreshness()
	if len(findings) != 1 {
		t.Fatalf("expected one ok finding, got %+v", findings)
	}
	if findings[0].Level != "ok" || !strings.Contains(findings[0].Detail, "match") {
		t.Fatalf("all-fresh slots must be ok, got %+v", findings[0])
	}
	if !strings.Contains(findings[0].Detail, "7") {
		t.Fatalf("ok finding should count the 7 compared slots, got %q", findings[0].Detail)
	}
}

func TestCheckGlobalRulesFreshnessReportsStale(t *testing.T) {
	redirectGlobalHome(t)
	paths := agentwire.GlobalRulesPaths()
	if len(paths) != 7 {
		t.Fatalf("GlobalRulesPaths() = %d paths, want 7", len(paths))
	}
	// Three stale (old 117-tool block), three fresh (canonical block).
	block := freshMarkedBlock(t)
	for i, p := range paths {
		if i < 3 {
			writeGlobalRulesFixture(t, p, staleMarkedBlock)
		} else {
			content := block
			if strings.HasSuffix(p, ".mdc") {
				content = agentwire.EnsureMDCFrontmatter(content)
			}
			writeGlobalRulesFixture(t, p, content)
		}
	}
	findings := checkGlobalRulesFreshness()
	if len(findings) != 3 {
		t.Fatalf("expected 3 stale warnings, got %+v", findings)
	}
	for _, f := range findings {
		if f.Level != "warn" || !strings.Contains(f.Detail, "kern setup --global-rules") {
			t.Errorf("stale finding must warn with the fix command, got %+v", f)
		}
	}
	for i := 0; i < 3; i++ {
		found := false
		for _, f := range findings {
			if strings.Contains(f.Detail, paths[i]) {
				found = true
			}
		}
		if !found {
			t.Errorf("stale file %s not reported in %+v", paths[i], findings)
		}
	}
	for i := 3; i < 6; i++ {
		for _, f := range findings {
			if strings.Contains(f.Detail, paths[i]) {
				t.Errorf("fresh file %s reported stale: %+v", paths[i], f)
			}
		}
	}
}

// TestCheckGlobalRulesFreshnessUnmarkedVariants covers the legacy unmarked
// "# kern usage rules" format (written by the old `kern setup --global`): a
// section carrying the current full asset is current, a section with stale
// content (the 117-tool text) is stale — the two files on this machine
// (~/.claude/CLAUDE.md, ~/.github/copilot-instructions.md) are the stale
// kind.
func TestCheckGlobalRulesFreshnessUnmarkedVariants(t *testing.T) {
	redirectGlobalHome(t)
	asset, err := agentwire.RulesFS().ReadFile("assets/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	staleUnmarked := "# kern usage rules\n\n`kern` ships 117 `kern_*` MCP tools across many domains. Only 6 are advertised by default (toolpolicy.go defaultTools).\n\n<!-- kern:global-omit:end -->\n"

	p := agentwire.GlobalRulesPaths()[0] // ~/.claude/CLAUDE.md
	writeGlobalRulesFixture(t, p, staleUnmarked)
	if got := checkGlobalRulesFreshness(); len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("stale unmarked slot must warn, got %+v", got)
	}

	writeGlobalRulesFixture(t, p, string(asset))
	if got := checkGlobalRulesFreshness(); len(got) != 1 || got[0].Level != "ok" {
		t.Fatalf("current unmarked slot must be ok, got %+v", got)
	}

	// A user-only file (no kern content) is not an installation: no findings.
	writeGlobalRulesFixture(t, p, "# my personal notes\n\nnot kern related\n")
	if got := checkGlobalRulesFreshness(); got != nil {
		t.Fatalf("user-only file must not be reported, got %+v", got)
	}
}
