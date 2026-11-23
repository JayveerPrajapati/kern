package setupundo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// isolatedHome points every home-relative resolver at a temp dir so removal
// tests can never touch the real machine.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("KERN_CACHE_DIR", "")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("KIRO_HOME", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	return home
}

func writeAt(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readAt(t *testing.T, home, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func existsAt(home, rel string) bool {
	_, err := os.Stat(filepath.Join(home, rel))
	return err == nil
}

// wireMachineWide mirrors what the installer runs: global-only wiring, the
// global rules slots, and the global instruction writers.
func wireMachineWide(t *testing.T, root string) {
	t.Helper()
	setup.WireWith(root, nil, false, true, setup.WireOptions{GlobalOnly: true})
	setup.WireGlobalRules()
	setup.WireGlobal(nil)
}

func assertNoFailures(t *testing.T, sts []setup.Status) {
	t.Helper()
	for _, s := range sts {
		if !s.Installed && !s.Skipped {
			t.Errorf("failed step %q (%s): %s", s.Agent, s.Path, s.Note)
		}
	}
}

func jsonValue(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

func assertJSONEqual(t *testing.T, home, rel, want string) {
	t.Helper()
	got := jsonValue(t, readAt(t, home, rel))
	if !reflect.DeepEqual(got, jsonValue(t, want)) {
		t.Errorf("%s changed beyond kern's entries.\n got: %s\nwant: %s", rel, readAt(t, home, rel), want)
	}
}

// userSeeds is pre-existing USER content in every kind of file kern edits.
// None of it belongs to kern, so removal must give it back intact.
func seedUserConfigs(t *testing.T, home string) {
	t.Helper()
	writeAt(t, home, ".claude/CLAUDE.md", "# My notes\n\nkeep me\n")
	writeAt(t, home, ".claude/settings.json", `{"theme":"dark","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/local/bin/my-hook"}]}]}}`)
	writeAt(t, home, "AGENTS.md", "# Mine\n\nhello\n")
	writeAt(t, home, ".codex/AGENTS.md", "codex personal\n")
	writeAt(t, home, ".config/opencode/AGENTS.md", "opencode personal\n")
	writeAt(t, home, ".codex/config.toml", "model = \"o3\"\n\n[mcp_servers.other]\ncommand = \"x\"\n\n[features]\nother = true\n")
	writeAt(t, home, ".cursor/mcp.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	writeAt(t, home, ".gemini/settings.json", `{"general":{"vimMode":true},"mcpServers":{"other":{"command":"y"}}}`)
	writeAt(t, home, ".config/opencode/opencode.jsonc", `{"theme":"x","mcp":{"other":{"type":"local"}}}`)
	writeAt(t, home, ".config/git/ignore", "*.log\n")
}

func TestRemoveGlobalRestoresSeededUserContent(t *testing.T) {
	home := isolatedHome(t)
	seedUserConfigs(t, home)
	root := t.TempDir()
	wireMachineWide(t, root)

	if !strings.Contains(readAt(t, home, ".claude/settings.json"), " guard hook") {
		t.Fatal("sanity: wiring did not install the claude hook")
	}
	if !strings.Contains(readAt(t, home, ".claude/CLAUDE.md"), "kern") {
		t.Fatal("sanity: wiring did not write the claude rules")
	}

	sts := RemoveGlobal(root, RemoveOptions{})
	if len(sts) == 0 {
		t.Fatal("RemoveGlobal reported nothing after a full wire")
	}
	assertNoFailures(t, sts)

	// Markdown: exact give-back.
	for rel, want := range map[string]string{
		".claude/CLAUDE.md":          "# My notes\n\nkeep me\n",
		"AGENTS.md":                  "# Mine\n\nhello\n",
		".codex/AGENTS.md":           "codex personal\n",
		".config/opencode/AGENTS.md": "opencode personal\n",
		".config/git/ignore":         "*.log\n",
	} {
		if got := readAt(t, home, rel); got != want {
			t.Errorf("%s not restored.\n got: %q\nwant: %q", rel, got, want)
		}
	}
	// JSON: semantically identical to the seed.
	assertJSONEqual(t, home, ".claude/settings.json", `{"theme":"dark","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/local/bin/my-hook"}]}]}}`)
	assertJSONEqual(t, home, ".cursor/mcp.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	assertJSONEqual(t, home, ".gemini/settings.json", `{"general":{"vimMode":true},"mcpServers":{"other":{"command":"y"}}}`)
	// TOML: user tables intact, kern tables gone (codex_hooks may remain).
	toml := readAt(t, home, ".codex/config.toml")
	for _, keep := range []string{`model = "o3"`, "[mcp_servers.other]", "other = true"} {
		if !strings.Contains(toml, keep) {
			t.Errorf("codex config lost %q:\n%s", keep, toml)
		}
	}
	if strings.Contains(toml, "mcp_servers.kern") {
		t.Errorf("codex config still has kern tables:\n%s", toml)
	}

	// Files kern created are gone.
	for _, rel := range []string{
		".cursor/hooks.json", ".qwen/settings.json", ".qoder/settings.json", ".codex/hooks.json",
		".copilot/hooks/kern-pretooluse.json", ".gemini/config/hooks.json", ".gemini/config/mcp_config.json",
		".kern/hooks/kern-guard.sh", ".cursor/rules/kern.mdc", ".claude/skills",
		".config/opencode/plugins/kern.ts", ".opencode/plugins/kern.ts",
	} {
		if existsAt(home, rel) {
			t.Errorf("%s should have been removed", rel)
		}
	}
	// Edited pre-existing files were backed up first.
	for _, rel := range []string{".claude/CLAUDE.md", ".claude/settings.json", "AGENTS.md", ".config/git/ignore"} {
		if m, _ := filepath.Glob(filepath.Join(home, rel+".bak.*")); len(m) == 0 {
			t.Errorf("no .bak.<ts> backup for %s", rel)
		}
	}
	// Nothing kern-owned remains anywhere (backups excluded).
	_ = filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.Contains(filepath.Base(p), ".bak") {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, needle := range []string{"kern-guard.sh", "kern usage rules", "mcp_servers.kern", "kern-version:", "kern:global-rules"} {
			if strings.Contains(string(b), needle) {
				t.Errorf("%s still contains %q", p, needle)
			}
		}
		return nil
	})
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output(); err == nil {
			t.Errorf("core.hooksPath still set to %q", strings.TrimSpace(string(out)))
		}
		if existsAt(home, ".kern/git-hooks") {
			t.Error("~/.kern/git-hooks should be gone")
		}
	}

	// Idempotent: the second run finds nothing.
	if again := RemoveGlobal(root, RemoveOptions{}); len(again) != 0 {
		t.Errorf("second RemoveGlobal should be a no-op, got %+v", again)
	}
}

func removeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if info.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = string(b)
		return nil
	})
	return out
}

func TestRemoveGlobalDryRunWritesNothing(t *testing.T) {
	home := isolatedHome(t)
	seedUserConfigs(t, home)
	root := t.TempDir()
	wireMachineWide(t, root)
	before := removeSnapshot(t, home)

	sts := RemoveGlobal(root, RemoveOptions{DryRun: true})
	if len(sts) == 0 {
		t.Fatal("dry-run reported nothing after a full wire")
	}
	for _, s := range sts {
		if s.Installed && !strings.Contains(s.Note, "[dry-run]") && !strings.HasPrefix(s.Agent, "claude-mcp") {
			t.Errorf("dry-run status does not say it is a preview: %+v", s)
		}
	}
	if after := removeSnapshot(t, home); !reflect.DeepEqual(before, after) {
		t.Error("dry-run changed the filesystem")
	}
}

func TestRemoveGlobalHooksOnlyPreservesOtherWiring(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	wireMachineWide(t, root)

	mcpBefore := readAt(t, home, ".cursor/mcp.json")
	rulesBefore := readAt(t, home, ".claude/CLAUDE.md")
	codexBefore := readAt(t, home, ".codex/config.toml")
	skillsDir := filepath.Join(home, ".claude", "skills")
	skillsBefore := removeSnapshot(t, skillsDir)

	sts := RemoveGlobal(root, RemoveOptions{HooksOnly: true})
	assertNoFailures(t, sts)
	if len(sts) == 0 {
		t.Fatal("hooks-only removal reported nothing after a full wire")
	}

	// Every agent hook surface and the shared hook script are gone.
	for _, rel := range []string{
		".cursor/hooks.json", ".codex/hooks.json",
		".copilot/hooks/kern-pretooluse.json", ".gemini/config/hooks.json",
		".kern/hooks/kern-guard.sh",
	} {
		if existsAt(home, rel) {
			t.Errorf("%s should be removed", rel)
		}
	}
	for _, rel := range []string{
		".qwen/settings.json",
		".qoder/settings.json", ".config/continue/settings.json",
	} {
		if existsAt(home, rel) {
			if got := readAt(t, home, rel); strings.Contains(got, "kern-guard") {
				t.Errorf("%s still contains a kern hook:\n%s", rel, got)
			}
		}
	}

	// MCP, rules, skills, and other agent wiring remain byte-for-byte.
	if got := readAt(t, home, ".cursor/mcp.json"); got != mcpBefore {
		t.Errorf("cursor MCP changed:\n got: %q\nwant: %q", got, mcpBefore)
	}
	if got := readAt(t, home, ".claude/CLAUDE.md"); got != rulesBefore {
		t.Errorf("claude rules changed:\n got: %q\nwant: %q", got, rulesBefore)
	}
	if got := readAt(t, home, ".codex/config.toml"); got != codexBefore {
		t.Errorf("codex config changed:\n got: %q\nwant: %q", got, codexBefore)
	}
	if got := removeSnapshot(t, skillsDir); !reflect.DeepEqual(got, skillsBefore) {
		t.Errorf("skills changed:\n got: %#v\nwant: %#v", got, skillsBefore)
	}
}

func TestRemoveGlobalAgentFilterLeavesOthers(t *testing.T) {
	home := isolatedHome(t)
	seedUserConfigs(t, home)
	root := t.TempDir()
	wireMachineWide(t, root)

	assertNoFailures(t, RemoveGlobal(root, RemoveOptions{Agents: []string{"cursor"}}))

	if existsAt(home, ".cursor/hooks.json") {
		t.Error("cursor hooks should be removed")
	}
	assertJSONEqual(t, home, ".cursor/mcp.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	// Other agents and the shared pieces stay wired.
	if !strings.Contains(readAt(t, home, ".claude/settings.json"), " guard hook") {
		t.Error("claude hook must survive a cursor-only removal")
	}
	if !existsAt(home, ".kern/hooks/kern-guard.sh") {
		t.Error("shared kern-guard script must survive a filtered removal")
	}
	if !strings.Contains(readAt(t, home, "AGENTS.md"), "kern") {
		t.Error("~/AGENTS.md rules must survive a filtered removal")
	}
}

func TestRemoveGlobalUnknownAgentChangesNothing(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	wireMachineWide(t, root)
	before := removeSnapshot(t, home)
	sts := RemoveGlobal(root, RemoveOptions{Agents: []string{"cursr"}})
	if len(sts) != 1 || sts[0].Installed || sts[0].Skipped || !strings.Contains(sts[0].Note, "unknown agent") {
		t.Fatalf("expected one unknown-agent failure, got %+v", sts)
	}
	if !reflect.DeepEqual(before, removeSnapshot(t, home)) {
		t.Error("an unknown agent must not remove anything")
	}
}

func TestRemoveGlobalLeavesForeignHooksPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := isolatedHome(t)
	if err := setup.InstallGlobalGitHooks(); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, "their-hooks")
	if out, err := exec.Command("git", "config", "--global", "core.hooksPath", foreign).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	sts := RemoveGlobal(t.TempDir(), RemoveOptions{})

	out, err := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if err != nil || strings.TrimSpace(string(out)) != foreign {
		t.Errorf("a foreign core.hooksPath must be left alone, got %q (%v)", out, err)
	}
	if existsAt(home, ".kern/git-hooks/pre-push") {
		t.Error("kern's own hook files should still be deleted")
	}
	found := false
	for _, s := range sts {
		if strings.Contains(s.Note, "not kern's") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a note about the foreign hooksPath, got %+v", sts)
	}
}

func TestRemoveGlobalKeepsNonKernFilesInGitHooksDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := isolatedHome(t)
	if err := setup.InstallGlobalGitHooks(); err != nil {
		t.Fatal(err)
	}
	writeAt(t, home, ".kern/git-hooks/pre-commit", "#!/bin/sh\necho my own hook\n")
	RemoveGlobal(t.TempDir(), RemoveOptions{})
	if got := readAt(t, home, ".kern/git-hooks/pre-commit"); !strings.Contains(got, "my own hook") {
		t.Errorf("a hook file kern did not write was deleted: %q", got)
	}
}

func TestRemoveGlobalLeavesUnbalancedRulesFileUntouched(t *testing.T) {
	home := isolatedHome(t)
	broken := "# mine\n\n" + agentwire.GlobalRulesMarkerOpen + "\nno close marker\n"
	writeAt(t, home, ".claude/CLAUDE.md", broken)
	sts := RemoveGlobal(t.TempDir(), RemoveOptions{Agents: []string{"claude"}})
	if got := readAt(t, home, ".claude/CLAUDE.md"); got != broken {
		t.Errorf("unbalanced file was modified: %q", got)
	}
	if len(sts) != 1 || !sts[0].Skipped || !strings.Contains(sts[0].Note, "left untouched") {
		t.Errorf("expected one 'left untouched' note, got %+v", sts)
	}
}

func TestRemoveGlobalLeavesInvalidJSONUntouched(t *testing.T) {
	home := isolatedHome(t)
	broken := `{"hooks": [this is not json`
	writeAt(t, home, ".claude/settings.json", broken)
	sts := RemoveGlobal(t.TempDir(), RemoveOptions{Agents: []string{"claude"}})
	if got := readAt(t, home, ".claude/settings.json"); got != broken {
		t.Errorf("invalid JSON was modified: %q", got)
	}
	if len(sts) == 0 || !sts[0].Skipped {
		t.Errorf("expected a skipped 'left untouched' status, got %+v", sts)
	}
}

func TestRemoveGlobalKeepsCustomizedPluginAndModifiedSkill(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	wireMachineWide(t, root)

	plugin := filepath.Join(home, ".config", "opencode", "plugins", "kern.ts")
	if err := os.WriteFile(plugin, []byte("// my customized plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var skillFile string
	_ = filepath.Walk(filepath.Join(home, ".claude", "skills"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) == "SKILL.md" && skillFile == "" {
			skillFile = p
		}
		return nil
	})
	if skillFile == "" {
		t.Fatal("sanity: no claude skill was installed")
	}
	if err := os.WriteFile(skillFile, []byte("my edited skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	assertNoFailures(t, RemoveGlobal(root, RemoveOptions{}))

	if b, err := os.ReadFile(plugin); err != nil || !strings.Contains(string(b), "customized plugin") {
		t.Error("a customized plugin must be left in place")
	}
	if b, err := os.ReadFile(skillFile); err != nil || string(b) != "my edited skill\n" {
		t.Error("a modified skill must be left in place")
	}
}

func TestRemoveGlobalNothingToRemoveOnCleanHome(t *testing.T) {
	isolatedHome(t)
	if sts := RemoveGlobal(t.TempDir(), RemoveOptions{}); len(sts) != 0 {
		t.Errorf("clean home should report nothing, got %+v", sts)
	}
}

// TestRemoveGlobalEditorExclusions pins the wireEditorExclusions reversal:
// kern's files.exclude["**/.kern"] entry is removed, every other user
// setting (including a user-set files.exclude map) survives, and a
// settings.json that held only kern's entry is deleted.
func TestRemoveGlobalEditorExclusions(t *testing.T) {
	home := isolatedHome(t)
	// Kern's exclusion inside a file the user also configured.
	writeAt(t, home, ".config/Code/User/settings.json", `{"files.exclude":{"**/.kern":true,"**/node_modules":true},"workbench.colorTheme":"dark"}`)
	// A settings file holding ONLY kern's entry (kern created it).
	writeAt(t, home, ".cursor/User/settings.json", `{"files.exclude":{"**/.kern":true}}`)

	sts := RemoveGlobal(t.TempDir(), RemoveOptions{})
	assertNoFailures(t, sts)

	got := readAt(t, home, ".config/Code/User/settings.json")
	if strings.Contains(got, ".kern") || !strings.Contains(got, "node_modules") || !strings.Contains(got, "colorTheme") {
		t.Errorf("VS Code settings wrong after removal: %s", got)
	}
	if existsAt(home, ".cursor/User/settings.json") {
		t.Error("cursor settings.json held only kern's entry — should be deleted")
	}

	// Idempotent, and a user-set value is never touched.
	writeAt(t, home, ".config/Code/User/settings.json", `{"files.exclude":{"**/.kern":false}}`)
	if sts := RemoveGlobal(t.TempDir(), RemoveOptions{}); len(sts) != 0 {
		t.Errorf("user-set exclusion should not be reported, got %+v", sts)
	}
	if got := readAt(t, home, ".config/Code/User/settings.json"); !strings.Contains(got, `"**/.kern":false`) {
		t.Errorf("user-set value must stay: %s", got)
	}
}
