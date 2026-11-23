package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
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

// TestCheckReportsGlobalRules verifies the setup report lists every global
// rules path, each labelled by its host (identical labels made the report
// look like duplicated rows — 2026-11-10 campaign).
func TestCheckReportsGlobalRules(t *testing.T) {
	testEnv(t)
	root := t.TempDir()
	sts := Check(root)
	seen := map[string]bool{}
	for _, s := range sts {
		if strings.HasPrefix(s.Agent, "global rules (") {
			seen[s.Agent] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("Check() reports %d distinct global-rules entries, want 7 (%v)", len(seen), seen)
	}
}

// TestThinAGENTSMDMode covers the default repo AGENTS.md variant (T-05
// compact policy, 2026-11-19): THIN (the self-sufficient core block) is the
// default everywhere — with or without a managed global slot; an existing
// FULL file is kept by default (never silently downgraded); full persisted
// in .kern/config.json on an explicit --agents-md=full; subsequent runs
// remember full; an explicit --agents-md=thin restores the thin variant.
func TestThinAGENTSMDMode(t *testing.T) {
	testEnv(t)
	root := t.TempDir()

	// Default run, no managed global slot: THIN rules (the T-05 core block
	// is self-sufficient), nothing persisted.
	WireWith(root, []string{"claude"}, false, false, WireOptions{})
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("default run without a managed global slot did not write the thin core rules:\n%s", b)
	}
	markerPath := filepath.Join(root, ".kern", "config.json")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("default run must persist explicit managed-project marker: %v", err)
	}
	var markerCfg struct {
		Managed bool `json:"managed"`
		Guard   struct {
			Enabled bool `json:"enabled"`
		} `json:"guard"`
	}
	if err := json.Unmarshal(marker, &markerCfg); err != nil || !markerCfg.Managed || !markerCfg.Guard.Enabled {
		t.Fatalf("default marker = %s err=%v, want managed=true and guard.enabled=true", marker, err)
	}

	// Managed global slots + an existing FULL file (e.g. the kern repo's own,
	// or a repo wired by a pre-T-05 setup): the default run KEEPS the full
	// rules — an unrequested default must not silently delete them; the
	// switch to thin is an explicit --agents-md thin choice (fresh wires
	// still get thin — TestProjectRulesThinWhenGlobalManaged).
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(agentwire.FullRulesHeader+"\nfull wiring\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	WireWith(root, []string{"claude"}, false, false, WireOptions{})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("default run with a managed global slot lost the full rules:\n%s", b)
	}
	if strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("thin rules written by default over existing full rules:\n%s", b)
	}

	// Explicit full: full rules written and persisted.
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "full"})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("full rules missing after explicit full:\n%s", b)
	}
	if strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("thin rules not stripped when switching to full:\n%s", b)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if !strings.Contains(string(cfg), `"agents_md": "full"`) {
		t.Fatalf("agents_md not persisted: %s", cfg)
	}

	// Subsequent run without the flag remembers full.
	WireWith(root, []string{"claude"}, false, false, WireOptions{})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("persisted full mode not honored on subsequent run:\n%s", b)
	}

	// Explicit thin restores the thin variant and persists the choice.
	WireWith(root, []string{"claude"}, false, false, WireOptions{AgentsMD: "thin"})
	b, _ = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), "# kern usage rules — thin (repo)") {
		t.Fatalf("thin mode not restored:\n%s", b)
	}
	if strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("full rules not stripped when switching to thin:\n%s", b)
	}
	cfg, _ = os.ReadFile(filepath.Join(root, ".kern", "config.json"))
	if !strings.Contains(string(cfg), `"agents_md": "thin"`) {
		t.Fatalf("thin not persisted: %s", cfg)
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
	WireWith(root, nil, false, false, WireOptions{AgentsMD: "full"})
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
