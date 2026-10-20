package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHostFiles seeds the repo with per-host rule files so the per-host
// mapping (CLAUDE.md -> ~/.claude/CLAUDE.md, GEMINI.md -> ~/.gemini/GEMINI.md)
// is exercised. setup must never CREATE these unprompted, but existing ones
// are updated.
func writeHostFiles(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"CLAUDE.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertVariant checks that a project rule file carries exactly the requested
// variant: "thin" (thin header present, full header absent) or "full" (full
// header present, thin header absent).
func assertVariant(t *testing.T, root, name, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if want == "thin" {
		if !strings.Contains(string(b), "# kern usage rules — thin (repo)") {
			t.Fatalf("%s not thin (managed global slot):\n%s", name, b)
		}
		if strings.Contains(string(b), "kern usage rules for agents") {
			t.Fatalf("%s carries the FULL rules despite a managed global slot:\n%s", name, b)
		}
		return
	}
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("%s not full (no managed global slot):\n%s", name, b)
	}
	if strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("%s is thin despite no managed global slot:\n%s", name, b)
	}
}

// TestProjectRulesThinWhenGlobalManaged pins the thin-by-default contract: a
// project whose corresponding host GLOBAL slots carry the kern-managed block
// (marker-based detection) gets THIN project rule files — the full rules load
// once via the global slot, so a thin repo file avoids loading the same rules
// twice in one session. This is the DEFAULT (no --agents-md flag, no
// persisted .kern/config.json choice).
func TestProjectRulesThinWhenGlobalManaged(t *testing.T) {
	testEnv(t) // sandboxed HOME + XDG_CONFIG_HOME
	root := t.TempDir()
	writeHostFiles(t, root)

	// Install the kern-managed block into all six global slots.
	WireGlobalRules()
	for _, p := range GlobalRulesPaths() {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("global slot not written: %s", p)
		}
	}

	Wire(root, []string{"mcp"}, false, false)

	assertVariant(t, root, "AGENTS.md", "thin")
	assertVariant(t, root, "CLAUDE.md", "thin")
	assertVariant(t, root, "GEMINI.md", "thin")
}

// TestProjectRulesFullWithoutGlobalSlot pins the fallback: with a sandboxed
// HOME carrying NO kern-managed global rules, the project rule files get the
// FULL rules — the repo file is the only delivery, so a thin file would leave
// agents without the kern-first policy.
func TestProjectRulesFullWithoutGlobalSlot(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	writeHostFiles(t, root)

	Wire(root, []string{"mcp"}, false, false)

	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		assertVariant(t, root, name, "full")
	}
}

// TestProjectRulesRerunStable pins byte-stability: running setup twice in a
// row is byte-identical in BOTH directions — thin stays thin (managed global
// slot) and full stays full (no global slot) — with no flip-flopping. The
// thin case also re-checks that the kern-managed content IS updated when the
// wired set changes (contract: setup updates the content it owns).
func TestProjectRulesRerunStable(t *testing.T) {
	t.Run("thin-with-global", func(t *testing.T) {
		testEnv(t)
		root := t.TempDir()
		WireGlobalRules()
		Wire(root, []string{"mcp"}, false, false)
		first, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(first), "Wired agents: mcp") {
			t.Fatalf("thin content missing wired facts:\n%s", first)
		}
		// Re-run, same inputs: byte-identical.
		Wire(root, []string{"mcp"}, false, false)
		second, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
		if string(first) != string(second) {
			t.Fatalf("thin re-run not byte-identical:\n--- first ---\n%s--- second ---\n%s", first, second)
		}
		// Changed wired set: the managed thin content is UPDATED, not skipped.
		Wire(root, []string{"mcp", "opencode"}, false, false)
		third, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
		if !strings.Contains(string(third), "Wired agents: mcp, opencode") {
			t.Fatalf("thin managed content not updated on a changed run:\n%s", third)
		}
	})
	t.Run("full-without-global", func(t *testing.T) {
		testEnv(t)
		root := t.TempDir()
		Wire(root, []string{"mcp"}, false, false)
		first, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		Wire(root, []string{"mcp"}, false, false)
		second, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
		if string(first) != string(second) {
			t.Fatalf("full re-run not byte-identical:\n--- first ---\n%s--- second ---\n%s", first, second)
		}
	})
}

// TestProjectRulesKeepFullWhenGlobalManaged pins the keep-full contract: once the
// host global slot carries the kern-managed block, the DEFAULT run still
// never downgrades an existing FULL rules file — a tracked full AGENTS.md
// (e.g. the kern repo's own) must stay byte-stable across `kern setup` runs.
// The dedup remediation applies only to fresh wires; an explicit thin choice
// still downgrades (TestThinReplacesFullRules).
func TestProjectRulesKeepFullWhenGlobalManaged(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	writeHostFiles(t, root)

	// Full default run first (no global slot yet).
	Wire(root, []string{"mcp"}, false, false)
	assertVariant(t, root, "AGENTS.md", "full")
	assertVariant(t, root, "CLAUDE.md", "full")

	// Global wiring arrives; the next default run KEEPS the existing full
	// rules (only --agents-md thin / a persisted choice downgrades).
	WireGlobalRules()
	Wire(root, []string{"mcp"}, false, false)
	assertVariant(t, root, "AGENTS.md", "full")
	assertVariant(t, root, "CLAUDE.md", "full")
	assertVariant(t, root, "GEMINI.md", "full")

	// A FRESH root wired while the global slot is managed still gets thin
	// (the dedup default; mirrors TestProjectRulesThinWhenGlobalManaged).
	fresh := t.TempDir()
	writeHostFiles(t, fresh)
	Wire(fresh, []string{"mcp"}, false, false)
	assertVariant(t, fresh, "AGENTS.md", "thin")
	assertVariant(t, fresh, "CLAUDE.md", "thin")
}
