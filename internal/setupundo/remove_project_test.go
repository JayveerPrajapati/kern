package setupundo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/setup"
)

// removeProjectFixture builds a repo with the full project wiring in place:
// the real Wire path (explicit agents so every repo-enabled target runs),
// pre-created agent marker dirs so instruction files and skill copies land
// too, plus deliberate user content that removal must preserve.
func removeProjectFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{
		filepath.Join(".git", "info"),
		".claude", ".cursor", ".gemini", ".github", ".agents",
		".opencode", ".codex",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	agents := []string{"opencode", "claude", "cursor", "antigravity", "continue", "windsurf", "kiro", "vscode", "copilot", "gemini", "codex"}
	for _, s := range setup.Wire(root, agents, false, false) {
		if s.Path == "" && s.Note != "" && !s.Installed {
			t.Fatalf("wire step %s failed: %s", s.Agent, s.Note)
		}
	}

	// User content that must survive removal.
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# user notes\n\nkeep me\n\n"+readFileT(t, filepath.Join(root, "AGENTS.md"))), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(root, ".mcp.json")
	var m map[string]any
	if err := json.Unmarshal([]byte(readFileT(t, mcpPath)), &m); err != nil {
		t.Fatal(err)
	}
	servers := m["mcpServers"].(map[string]any)
	servers["other"] = map[string]any{"command": "other-bin"}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mcpPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gi, []byte("/user-build\n\n"+readFileT(t, gi)), 0o644); err != nil {
		t.Fatal(err)
	}
	exc := filepath.Join(root, ".git", "info", "exclude")
	if err := os.WriteFile(exc, []byte("# user excludes\n/user-local\n"+readFileT(t, exc)), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestRemoveProjectRoundTrip(t *testing.T) {
	root := removeProjectFixture(t)

	snapshot := func() map[string]string {
		got := map[string]string{}
		for _, p := range []string{
			".mcp.json", "AGENTS.md", ".gitignore", ".git/info/exclude",
			"opencode.json", ".vscode/mcp.json", ".agents/hooks.json",
			".opencode/plugins/kern.ts", ".kern/profiles.json",
		} {
			if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
				got[p] = string(b)
			}
		}
		return got
	}
	before := snapshot()

	// Dry-run: nothing changes.
	for _, s := range RemoveProject(root, RemoveOptions{DryRun: true}) {
		if s.Path != "" && !s.Installed && !s.Skipped {
			t.Fatalf("dry-run step %s failed: %s", s.Agent, s.Note)
		}
	}
	for p, want := range before {
		if got := readFileT(t, filepath.Join(root, p)); got != want {
			t.Fatalf("dry-run modified %s", p)
		}
	}

	// Real removal.
	removed := 0
	for _, s := range RemoveProject(root, RemoveOptions{}) {
		if s.Note != "" && strings.Contains(s.Note, "left untouched:") {
			t.Fatalf("unexpected failure: %+v", s)
		}
		removed++
	}
	if removed == 0 {
		t.Fatal("removal reported nothing")
	}

	// .mcp.json keeps the user's other server, loses kern's.
	mcp := readFileT(t, filepath.Join(root, ".mcp.json"))
	if !strings.Contains(mcp, "other-bin") || strings.Contains(mcp, `"kern"`) {
		t.Fatalf("mcp.json after remove:\n%s", mcp)
	}
	// AGENTS.md keeps the user section only.
	agentsMD := readFileT(t, filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(agentsMD, "keep me") || strings.Contains(agentsMD, "kern usage rules") {
		t.Fatalf("AGENTS.md after remove:\n%s", agentsMD)
	}
	// .gitignore keeps the user line, loses the kern block.
	gi := readFileT(t, filepath.Join(root, ".gitignore"))
	if !strings.Contains(gi, "/user-build") || strings.Contains(gi, "kern generated") {
		t.Fatalf(".gitignore after remove:\n%s", gi)
	}
	// The local exclude keeps user lines, loses the kern pair.
	exc := readFileT(t, filepath.Join(root, ".git", "info", "exclude"))
	if !strings.Contains(exc, "# user excludes") || strings.Contains(exc, "kern local exclude") || strings.Contains(exc, ".kern/") {
		t.Fatalf("exclude after remove:\n%s", exc)
	}
	// Kern-only files are gone entirely.
	for _, p := range []string{
		"opencode.json", ".vscode/mcp.json", ".agents/hooks.json",
		".opencode/plugins/kern.ts", ".kern/profiles.json",
		".agents/skills", ".claude/skills/kern-investigate",
		".cursor/rules/kern-hooks.mdc", ".cursor/rules/kern-investigate.mdc",
		".continue/rules/kern.md", ".windsurf/rules/kern-first.md",
		".kiro/steering/kern-first.md",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Fatalf("%s still present after remove", p)
		}
	}
	// Instruction files (CLAUDE.md etc.) are stripped of the marked block.
	if cl := filepath.Join(root, "CLAUDE.md"); fileExists(cl) {
		if strings.Contains(readFileT(t, cl), "kern-instruction") {
			t.Fatalf("CLAUDE.md still carries the kern block:\n%s", readFileT(t, cl))
		}
	}

	// A second remove run is a quiet no-op, not an error.
	again := RemoveProject(root, RemoveOptions{})
	for _, s := range again {
		if !s.Installed && !s.Skipped && s.Note != "" && !strings.Contains(s.Note, "not present") && !strings.Contains(s.Note, "no kern") {
			// editText on missing files reports nothing; only hard errors matter.
			t.Fatalf("second remove reported a failure: %+v", s)
		}
	}
}

func TestRemoveProjectHooksOnlyPreservesOtherWiring(t *testing.T) {
	root := removeProjectFixture(t)
	hooks := filepath.Join(root, ".agents", "hooks.json")
	hookJSON := []byte(`{"other-guard":{"PreToolUse":[{"hooks":[{"type":"command","command":"other hook"}]}]},"kern-guard":{"PreToolUse":[{"hooks":[{"type":"command","command":"kern guard hook"}]}]}}` + "\n")
	if err := os.WriteFile(hooks, hookJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		got := map[string]string{}
		for _, p := range []string{
			".mcp.json", "AGENTS.md", ".gitignore", ".git/info/exclude",
			"opencode.json", ".vscode/mcp.json", ".opencode/plugins/kern.ts",
			".kern/profiles.json",
		} {
			if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
				got[p] = string(b)
			}
		}
		return got
	}
	before := snapshot()

	sts := RemoveProject(root, RemoveOptions{HooksOnly: true})
	if len(sts) == 0 {
		t.Fatal("hooks-only project removal reported nothing")
	}
	got := readFileT(t, hooks)
	if strings.Contains(got, "kern-guard") || !strings.Contains(got, "other-guard") {
		t.Fatalf("project hooks after remove:\n%s", got)
	}
	if got := snapshot(); !reflect.DeepEqual(got, before) {
		t.Errorf("non-hook project wiring changed:\n got: %#v\nwant: %#v", got, before)
	}
}

func TestRemoveProjectKeepsCustomizedPlugin(t *testing.T) {
	root := removeProjectFixture(t)
	p := filepath.Join(root, ".opencode", "plugins", "kern.ts")
	custom := []byte("// customized by user\n" + readFileT(t, p))
	if err := os.WriteFile(p, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range RemoveProject(root, RemoveOptions{}) {
		if s.Agent == "opencode-plugin" {
			found = true
			if !strings.Contains(s.Note, "customized") {
				t.Fatalf("plugin removal note: %s", s.Note)
			}
		}
	}
	if !found {
		t.Fatal("plugin step missing from removal statuses")
	}
	if got := readFileT(t, p); got != string(custom) {
		t.Fatal("customized plugin was modified")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestRemoveProjectPrunesEmptyAgentDirs (F-A10): removing the project wiring
// must also remove the agent directories that wiring created and then
// emptied — a fresh setup→remove round-trip used to strand empty .agents/
// and .opencode/. User content inside those dirs keeps them alive.
func TestRemoveProjectPrunesEmptyAgentDirs(t *testing.T) {
	root := t.TempDir()
	// A fresh repo: no pre-existing agent dirs — the wiring creates
	// .agents/ and .opencode/ itself. (.git/info must exist so the local
	// git-exclude step has somewhere to write.)
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := []string{"opencode", "antigravity"}
	for _, s := range setup.Wire(root, agents, false, false) {
		if s.Path == "" && s.Note != "" && !s.Installed {
			t.Fatalf("wire step %s failed: %s", s.Agent, s.Note)
		}
	}
	if !fileExists(filepath.Join(root, ".agents")) {
		t.Fatal("sanity: wiring did not create .agents")
	}
	if !fileExists(filepath.Join(root, ".opencode")) {
		t.Fatal("sanity: wiring did not create .opencode")
	}
	// User content in .agents must keep that directory alive.
	notes := filepath.Join(root, ".agents", "team-notes.md")
	if err := os.WriteFile(notes, []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, s := range RemoveProject(root, RemoveOptions{}) {
		if s.Path != "" && !s.Installed && !s.Skipped {
			t.Fatalf("remove step %s failed: %+v", s.Agent, s)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode")); !os.IsNotExist(err) {
		t.Errorf("an emptied .opencode/ must be pruned (stat err=%v)", err)
	}
	if !fileExists(notes) {
		t.Fatal("user content inside .agents must survive the removal")
	}
	if !fileExists(filepath.Join(root, ".agents")) {
		t.Error(".agents/ holding user content must stay")
	}
	if !fileExists(filepath.Join(root, ".kern", "config.json")) {
		t.Error(".kern data must be left untouched by the project removal")
	}

	// A second run must stay a quiet no-op (nothing left to prune).
	for _, s := range RemoveProject(root, RemoveOptions{}) {
		if s.Agent == "dirs (project)" {
			t.Errorf("second removal must not report dir pruning again: %+v", s)
		}
	}
}
