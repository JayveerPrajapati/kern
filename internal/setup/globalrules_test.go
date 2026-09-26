package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv redirects every user-global path under a temp HOME (and a temp
// XDG_CONFIG_HOME for the opencode global path) so the real ~/.claude,
// ~/.codex and ~/.config/opencode are NEVER touched by these tests.
func testEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestGlobalRulesPaths pins the three host global instruction slots.
func TestGlobalRulesPaths(t *testing.T) {
	home := testEnv(t)
	paths := GlobalRulesPaths()
	want := []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".config", "opencode", "AGENTS.md"),
	}
	if len(paths) != len(want) {
		t.Fatalf("GlobalRulesPaths() = %d entries, want %d", len(paths), len(want))
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// TestWireGlobalRulesCreatesMissing verifies missing global instruction files
// are created with the canonical marker-delimited block.
func TestWireGlobalRulesCreatesMissing(t *testing.T) {
	testEnv(t)
	sts := WireGlobalRules()
	if len(sts) != 3 {
		t.Fatalf("WireGlobalRules() = %d statuses, want 3", len(sts))
	}
	for _, p := range GlobalRulesPaths() {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("global rules not written for %s: %v", p, err)
		}
		content := string(b)
		if !strings.Contains(content, globalRulesMarkerOpen) || !strings.Contains(content, globalRulesMarkerClose) {
			t.Fatalf("%s missing markers", p)
		}
		want, _ := globalRulesFS.ReadFile("assets/global-rules.md")
		if string(b) != string(want) {
			t.Fatalf("%s content differs from canonical block", p)
		}
	}
	for _, s := range sts {
		if !s.Installed {
			t.Errorf("status not installed: %+v", s)
		}
	}
}

// TestWireGlobalRulesPreservesUserContent verifies content outside the
// markers survives a re-run verbatim.
func TestWireGlobalRulesPreservesUserContent(t *testing.T) {
	testEnv(t)
	WireGlobalRules()
	p := GlobalRulesPaths()[0]
	userPrefs := "# my personal notes\n\nalways use python3 for scripts\n"
	orig, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(userPrefs+string(orig)), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if !strings.HasPrefix(content, userPrefs) {
		t.Fatalf("user content outside markers not preserved verbatim:\n%s", content)
	}
	if strings.Count(content, globalRulesMarkerOpen) != 1 {
		t.Fatalf("kern block duplicated:\n%s", content)
	}
}

// TestWireGlobalRulesIdempotent verifies a re-run with nothing else changed
// produces identical bytes.
func TestWireGlobalRulesIdempotent(t *testing.T) {
	testEnv(t)
	WireGlobalRules()
	first := map[string]string{}
	for _, p := range GlobalRulesPaths() {
		b, _ := os.ReadFile(p)
		first[p] = string(b)
	}
	WireGlobalRules()
	for _, p := range GlobalRulesPaths() {
		b, _ := os.ReadFile(p)
		if string(b) != first[p] {
			t.Fatalf("%s changed on re-run", p)
		}
	}
}

// TestWireGlobalRulesReplacesStaleBlock verifies a stale kern-managed block
// is replaced in place while content outside the markers survives.
func TestWireGlobalRulesReplacesStaleBlock(t *testing.T) {
	testEnv(t)
	p := GlobalRulesPaths()[0]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := globalRulesMarkerOpen + "\nOLD STALE RULES\n" + globalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte("keep me\n\n"+stale), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if strings.Contains(content, "OLD STALE RULES") {
		t.Fatalf("stale block not replaced:\n%s", content)
	}
	if !strings.HasPrefix(content, "keep me\n") {
		t.Fatalf("content outside markers lost:\n%s", content)
	}
	want, _ := globalRulesFS.ReadFile("assets/global-rules.md")
	if !strings.Contains(content, string(want)) {
		t.Fatalf("fresh canonical block missing:\n%s", content)
	}
}

// TestWireGlobalRulesAppendsWithoutMarkers verifies a file with no markers
// gets the block appended behind a blank separator, user content intact.
func TestWireGlobalRulesAppendsWithoutMarkers(t *testing.T) {
	testEnv(t)
	p := GlobalRulesPaths()[1]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user rules without markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if !strings.HasPrefix(content, "user rules without markers\n\n") {
		t.Fatalf("user content + blank separator missing:\n%s", content)
	}
	if !strings.Contains(content, globalRulesMarkerOpen) {
		t.Fatalf("kern block not appended:\n%s", content)
	}
}

// TestCheckReportsGlobalRules verifies the setup report lists every global
// rules path.
func TestCheckReportsGlobalRules(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	sts := Check(root)
	found := 0
	for _, s := range sts {
		if s.Agent == "global rules" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("Check() reports %d global-rules entries, want 3", found)
	}
}

// TestThinAGENTSMDMode covers the opt-in thin repo AGENTS.md: full default,
// thin persisted in .kern/config.json, subsequent runs remember, and full is
// restored on an explicit --agents-md=full.
func TestThinAGENTSMDMode(t *testing.T) {
	testEnv(t)
	root := t.TempDir()

	// Default run: full rules, nothing persisted.
	WireWith(root, []string{"claude"}, false, false, WireOptions{})
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "kern usage rules") {
		t.Fatalf("default run did not write full rules:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("default run must not persist config.json")
	}

	// Explicit thin: thin AGENTS.md with the pointer line, persisted.
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "thin"})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "Full kern usage rules live in your agent's global instructions (managed by kern setup --global-rules).") {
		t.Fatalf("thin pointer line missing:\n%s", b)
	}
	if strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("full rules not stripped in thin mode:\n%s", b)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if !strings.Contains(string(cfg), `"agents_md": "thin"`) {
		t.Fatalf("agents_md not persisted: %s", cfg)
	}

	// Subsequent run without the flag remembers thin.
	WireWith(root, []string{"claude"}, false, false, WireOptions{})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "Full kern usage rules live in your agent's global instructions") {
		t.Fatalf("persisted thin mode not honored on subsequent run:\n%s", b)
	}

	// Explicit full restores the full rules and persists the choice.
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "full"})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(b), "Full kern usage rules live in your agent's global instructions") {
		t.Fatalf("full mode not restored:\n%s", b)
	}
	if !strings.Contains(string(b), "kern usage rules") {
		t.Fatalf("full rules missing after restore:\n%s", b)
	}
	cfg, _ = os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if !strings.Contains(string(cfg), `"agents_md": "full"`) {
		t.Fatalf("full not persisted: %s", cfg)
	}
}

// TestThinAGENTSMDIdempotent verifies the thin file stays under ~15 lines and
// a re-run produces identical bytes.
func TestThinAGENTSMDIdempotent(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "thin"})
	first, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if lines := strings.Count(string(first), "\n"); lines > 15 {
		t.Fatalf("thin AGENTS.md too long: %d lines\n%s", lines, first)
	}
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "thin"})
	second, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if string(first) != string(second) {
		t.Fatalf("thin re-run not idempotent:\n--- first ---\n%s--- second ---\n%s", first, second)
	}
}

// TestThinReplacesFullRules verifies switching full -> thin strips the old
// full kern section rather than stacking both.
func TestThinReplacesFullRules(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	WireWith(root, nil, false, false, WireOptions{})
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("full rules expected before switch:\n%s", b)
	}
	WireWith(root, nil, false, false, WireOptions{AgentsMD: "thin"})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("full rules not stripped when switching to thin:\n%s", b)
	}
	if strings.Count(string(b), "# kern usage rules") != 1 {
		t.Fatalf("expected exactly one kern section:\n%s", b)
	}
}

// TestSetAgentsMDPreservesOtherKeys verifies persisting agents_md merges into
// an existing .kern/config.json instead of clobbering unrelated keys.
func TestSetAgentsMDPreservesOtherKeys(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(`{"llm":{"model":"kern-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setAgentsMD(root, "thin"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if !strings.Contains(string(b), `"kern-1"`) {
		t.Fatalf("existing keys lost: %s", b)
	}
	if !strings.Contains(string(b), `"agents_md": "thin"`) {
		t.Fatalf("agents_md missing: %s", b)
	}
	// Idempotent persist: same value -> no rewrite.
	if err := setAgentsMD(root, "thin"); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if string(b2) != string(b) {
		t.Fatalf("re-persist changed bytes: %s -> %s", b, b2)
	}
}
