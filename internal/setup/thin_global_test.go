package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
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
			t.Fatalf("%s not thin:\n%s", name, b)
		}
		if strings.Contains(string(b), "kern usage rules for agents") {
			t.Fatalf("%s carries the FULL rules:\n%s", name, b)
		}
		return
	}
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("%s not full:\n%s", name, b)
	}
	if strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("%s is thin:\n%s", name, b)
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

// TestProjectRulesThinByDefaultWithoutGlobalSlot pins the T-05 compact
// policy (2026-11-19): with a sandboxed HOME carrying NO kern-managed global
// rules, the project rule files STILL get the THIN (core) variant — the
// core block is self-sufficient (routing, blast radius, verify, fallback,
// guard boundaries), so the default repo policy stays <=2KB everywhere.
// The long policy is opt-in via --agents-md full.
func TestProjectRulesThinByDefaultWithoutGlobalSlot(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	writeHostFiles(t, root)

	Wire(root, []string{"mcp"}, false, false)

	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		assertVariant(t, root, name, "thin")
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
	t.Run("thin-without-global", func(t *testing.T) {
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
			t.Fatalf("thin re-run not byte-identical:\n--- first ---\n%s--- second ---\n%s", first, second)
		}
	})
}

// TestProjectRulesKeepFullWhenGlobalManaged pins the keep-full contract: the
// DEFAULT run never downgrades an existing FULL rules file — a tracked full
// AGENTS.md (e.g. the kern repo's own, or a repo wired by a pre-T-05 setup)
// must stay byte-stable across `kern setup` runs. Downgrade requires an
// explicit choice (TestThinReplacesFullRules); a fresh root gets thin.
func TestProjectRulesKeepFullWhenGlobalManaged(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	writeHostFiles(t, root)

	// Seed the full variant the pre-T-05 default would have written (a full
	// header + marker-managed block), then verify default runs keep it.
	full := agentwire.FullRulesHeader + "\npre-T-05 full wiring\n"
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(full), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Default run (with the global slot managed): KEEPS the existing full
	// rules (only --agents-md thin / a persisted choice downgrades).
	WireGlobalRules()
	Wire(root, []string{"mcp"}, false, false)
	assertVariant(t, root, "AGENTS.md", "full")
	assertVariant(t, root, "CLAUDE.md", "full")
	assertVariant(t, root, "GEMINI.md", "full")

	// A FRESH root wired by the default still gets thin (the T-05 compact
	// default; mirrors TestProjectRulesThinWhenGlobalManaged).
	fresh := t.TempDir()
	writeHostFiles(t, fresh)
	Wire(fresh, []string{"mcp"}, false, false)
	assertVariant(t, fresh, "AGENTS.md", "thin")
	assertVariant(t, fresh, "CLAUDE.md", "thin")
}
