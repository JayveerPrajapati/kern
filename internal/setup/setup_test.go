package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func TestMergeJSONAddsKern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := mergeJSON(path, "mcp", map[string]any{"type": "local", "command": []string{"/x/kern-mcp"}, "enabled": true}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mcp, _ := m["mcp"].(map[string]any)
	if _, ok := mcp["kern"]; !ok {
		t.Fatal("kern entry missing after merge")
	}
}

func TestMergeJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	entry := map[string]any{"type": "local", "command": []string{"/x/kern-mcp"}, "enabled": true}
	if err := mergeJSON(path, "mcp", entry); err != nil {
		t.Fatal(err)
	}
	// Second merge must not duplicate or replace the entry.
	if err := mergeJSON(path, "mcp", entry); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "/x/kern-mcp") != 1 {
		t.Fatalf("entry duplicated: %s", b)
	}
}

func TestMergeJSONRepairsStaleKern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	oldEntry := map[string]any{"type": "local", "command": []string{"/stale/kern-mcp"}, "enabled": true}
	if err := mergeJSON(path, "mcp", oldEntry); err != nil {
		t.Fatal(err)
	}
	// Binary moved: the entry must be repaired, not left stale.
	newEntry := map[string]any{"type": "local", "command": []string{"/new/kern-mcp"}, "enabled": true}
	if err := mergeJSON(path, "mcp", newEntry); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/new/kern-mcp") || strings.Contains(string(b), "/stale/kern-mcp") {
		t.Fatalf("stale kern entry not repaired: %s", b)
	}
}

func TestMergeJSONPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte(`{"$schema":"https://opencode.ai/config.json","model":"fast"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSON(path, "mcp", map[string]any{"type": "local"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"$schema"`, `"model": "fast"`, `"kern"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("merge dropped %s: %s", want, b)
		}
	}
}

func TestMergeJSONInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json {"), 0o644)
	if err := mergeJSON(path, "mcp", map[string]any{}); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWireCreatesProjectFiles(t *testing.T) {
	dir := t.TempDir()
	sts := Wire(dir, []string{"mcp", "opencode"})
	if !allInstalled(sts, "mcp") {
		t.Fatalf("mcp not installed: %+v", sts)
	}
	for _, f := range []string{".mcp.json", "opencode.json", ".opencode/plugins/kern.ts", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}

func TestWireIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	Wire(dir, []string{"mcp", "opencode"})
	Wire(dir, []string{"mcp", "opencode"})
	b, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if strings.Count(string(b), `"kern":`) != 1 {
		t.Fatalf("duplicated kern entries: %s", b)
	}
	if strings.Count(string(b), "kern-mcp") != 1 {
		t.Fatalf("duplicated kern-mcp command: %s", b)
	}
	b2, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Count(string(b2), "kern usage rules") != 1 {
		t.Fatalf("duplicated AGENTS rules: %s", b2)
	}
}

func TestWirePeerAgentRules(t *testing.T) {
	dir := t.TempDir()
	// Existing host files get the same single-source rules; setup must not
	// create host rule files that do not exist.
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Claude\n"), 0o644)
	sts := Wire(dir, []string{"mcp", "opencode"})
	if !allInstalled(sts, "opencode") {
		t.Fatalf("opencode not installed: %+v", sts)
	}
	// CLAUDE.md (existing) received the rules; GEMINI.md (absent) was not made.
	c, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(c), "kern usage rules") {
		t.Fatalf("CLAUDE.md missing kern rules: %s", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); err == nil {
		t.Fatal("setup must not create GEMINI.md unprompted")
	}
	// Idempotent: a second run appends nothing.
	Wire(dir, []string{"mcp", "opencode"})
	c2, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if strings.Count(string(c2), "kern usage rules") != 1 {
		t.Fatalf("CLAUDE.md rules duplicated: %s", c2)
	}
}

func TestCheckReports(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Check(dir)
	if len(sts) == 0 {
		t.Fatal("Check returned nothing")
	}
	for _, s := range sts {
		if s.Installed {
			t.Fatalf("fresh dir should not report installed: %+v", s)
		}
	}
}

// TestWireAllAgents wires every JSON-config adapter and asserts each produced
// config contains a valid kern entry pointing at the kern-mcp binary. This is
// the invariant that "all tools reach all agents": every agent consumes the
// same MCP server, so the 46-tool catalog is universally available the moment
// each config registers kern.
func TestWireAllAgents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Wire(dir, nil)

	bin := Bin()
	home := os.Getenv("HOME")
	wired := map[string]Status{}
	for _, s := range sts {
		wired[s.Agent] = s
	}

	// Project-level adapters write inside the repo; global/home adapters write
	// under the redirected config dirs. Claude needs its CLI on PATH: with a
	// fresh HOME it is absent, which is an expected graceful skip, not a
	// config we can assert on.
	for _, a := range adapters {
		path := a.path(dir)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("agent %s: config not written: %v", a.name, err)
			continue
		}
		if !strings.Contains(string(b), "kern") || !strings.Contains(string(b), bin) {
			t.Errorf("agent %s: config %s lacks kern entry for %s:\n%s", a.name, path, bin, b)
		}
	}

	if !allInstalled(sts, "codex") {
		t.Errorf("codex not wired: %+v", sts)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil || !strings.Contains(string(b), "[mcp_servers.kern]") {
		t.Errorf("codex toml missing kern MCP: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".opencode", "plugins", "kern.ts")); err != nil {
		t.Errorf("opencode plugin not installed: %v", err)
	}
}

// pluginToolRe extracts opencode plugin tool definitions (kern_xxx: tool({
// with optional leading whitespace).
var pluginToolRe = regexp.MustCompile(`kern_[a-zA-Z0-9_]+:\s*tool\(`)

// TestPluginMatchesMCPCatalog is the parity invariant behind "all tools reach
// all agents". Every agent consumes the MCP server via tools/list — that is
// the source of truth. The opencode plugin and the on-disk .opencode copy must
// each expose exactly the same set, so no surface silently lags the universal
// catalog.
func TestPluginMatchesMCPCatalog(t *testing.T) {
	mcpSet := map[string]bool{}
	for _, n := range mcp.ToolNames() {
		if !strings.HasPrefix(n, "kern_") {
			t.Fatalf("MCP tool name missing kern_ prefix: %s", n)
		}
		mcpSet[n] = true
	}
	if len(mcpSet) < 40 {
		t.Fatalf("suspiciously small MCP catalog: %d tools", len(mcpSet))
	}

	// Compare both the embedded plugin asset (what `kern setup` installs) and
	// the live repo copy so neither can drift from the MCP catalog.
	readSrc := func(label string) (string, error) {
		switch label {
		case "embedded":
			b, err := pluginFS.ReadFile("assets/plugin/kern.ts")
			return string(b), err
		case "repo":
			b, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "plugins", "kern.ts"))
			return string(b), err
		}
		return "", os.ErrNotExist
	}
	for _, src := range []string{"embedded", "repo"} {
		content, err := readSrc(src)
		if err != nil {
			t.Fatalf("read %s plugin: %v", src, err)
		}
		pluginSet := map[string]bool{}
		for _, m := range pluginToolRe.FindAllString(content, -1) {
			name := strings.TrimSuffix(m, ": tool(")
			if !strings.HasPrefix(name, "kern_") {
				t.Fatalf("plugin tool missing kern_ prefix: %s", name)
			}
			pluginSet[name] = true
		}
		for n := range mcpSet {
			if !pluginSet[n] {
				t.Errorf("%s: plugin missing MCP tool %s", src, n)
			}
		}
		for n := range pluginSet {
			if !mcpSet[n] {
				t.Errorf("%s: plugin tool %s not in MCP catalog (stale or invalid)", src, n)
			}
		}
	}

	// The embedded asset must be byte-identical to the live repo copy:
	// `kern setup` installs the asset, so any bugfix applied to the working
	// plugin but not synced would ship to every user while passing the
	// name-parity checks above.
	emb, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatalf("read embedded plugin: %v", err)
	}
	repo, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "plugins", "kern.ts"))
	if err != nil {
		t.Fatalf("read repo plugin: %v", err)
	}
	if !bytes.Equal(emb, repo) {
		t.Error("embedded plugin asset drifted from .opencode/plugins/kern.ts — run: cp .opencode/plugins/kern.ts internal/setup/assets/plugin/kern.ts")
	}
}

func allInstalled(sts []Status, agent string) bool {
	for _, s := range sts {
		if s.Agent == agent && !s.Installed {
			return false
		}
	}
	return true
}

func TestWireClaudeHooks(t *testing.T) {
	dir := t.TempDir()
	st := wireClaudeHooks(dir, "/x/kern")
	if !st.Installed {
		t.Fatalf("install failed: %s", st.Note)
	}
	var m map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid claude settings: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)
	post, _ := hooks["PostToolUse"].([]any)
	if len(post) != 1 {
		t.Fatalf("expected 1 PostToolUse group, got %d", len(post))
	}
	cmd := hookCommandOf(post[0])
	if !strings.Contains(cmd, "claude-post") || !strings.Contains(cmd, "$CLAUDE_PROJECT_DIR") {
		t.Errorf("unexpected command %q", cmd)
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Error("UserPromptSubmit hook missing")
	}

	// Idempotent: a second run must not duplicate the group.
	if st := wireClaudeHooks(dir, "/x/kern"); !st.Installed {
		t.Fatalf("second install failed: %s", st.Note)
	}
	b, _ = os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	json.Unmarshal(b, &m)
	hooks, _ = m["hooks"].(map[string]any)
	if post, _ := hooks["PostToolUse"].([]any); len(post) != 1 {
		t.Fatalf("re-run duplicated PostToolUse groups: %d", len(post))
	}
}

func TestWireGeminiHooksPreservesMCPServers(t *testing.T) {
	dir := t.TempDir()
	// A pre-existing .gemini/settings.json with mcpServers (as the adapter
	// writer produces) must keep that key when hooks are merged in.
	gpath := filepath.Join(dir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(gpath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{"mcpServers": map[string]any{"kern": map[string]any{"type": "stdio"}}}
	b, _ := json.Marshal(existing)
	os.WriteFile(gpath, b, 0o644)

	st := wireGeminiHooks(dir, "/x/kern")
	if !st.Installed {
		t.Fatalf("install failed: %s", st.Note)
	}
	var m map[string]any
	b, _ = os.ReadFile(gpath)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid gemini settings: %v", err)
	}
	if _, ok := m["mcpServers"].(map[string]any); !ok {
		t.Fatal("mcpServers was clobbered by hook merge")
	}
	hooks, _ := m["hooks"].(map[string]any)
	if _, ok := hooks["AfterTool"]; !ok {
		t.Fatal("gemini uses AfterTool (not PostToolUse); missing")
	}
	if _, ok := hooks["PostToolUse"]; ok {
		t.Fatal("PostToolUse is the Claude event name and is skipped by Gemini — must not be written")
	}
	var after []any
	after, _ = hooks["AfterTool"].([]any)
	if len(after) == 0 {
		t.Fatal("AfterTool group missing")
	}
	cmd := hookCommandOf(after[0])
	if !strings.Contains(cmd, "gemini-after") || !strings.Contains(cmd, "$GEMINI_PROJECT_DIR") {
		t.Errorf("unexpected command %q", cmd)
	}
	if _, ok := hooks["BeforeAgent"]; !ok {
		t.Error("BeforeAgent hook missing")
	}
}

func TestGitignoreGenerated(t *testing.T) {
	dir := t.TempDir()
	// Existing .gitignore content is preserved.
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644)
	st := gitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(b)
	if !strings.Contains(content, ".mcp.json") || !strings.Contains(content, ".claude/") {
		t.Fatalf("generated entries missing:\n%s", content)
	}
	if !strings.HasPrefix(content, "bin/\n") {
		t.Fatal("existing .gitignore content was not preserved")
	}
	// Idempotent: second run adds nothing.
	before := content
	gitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("gitignore block duplicated on re-run")
	}
}

func TestWireCursorRules(t *testing.T) {
	dir := t.TempDir()
	st := wireCursorRules(dir)
	if !st.Installed {
		t.Fatalf("cursor rule failed: %s", st.Note)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".cursor", "rules", "kern-hooks.mdc"))
	if !strings.Contains(string(b), "kern_optimize_log") || !strings.Contains(string(b), "kern_memory_add") {
		t.Fatal("cursor rule missing kern tool guidance")
	}
	// Idempotent.
	st2 := wireCursorRules(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".cursor", "rules", "kern-hooks.mdc"))
	if strings.Count(string(b), "kern_optimize_log") > 1 {
		t.Fatal("cursor rule duplicated on re-run")
	}
	_ = st2
}

func hookCommandOf(group any) string {
	gm, _ := group.(map[string]any)
	if hs, _ := gm["hooks"].([]any); len(hs) > 0 {
		if hm, _ := hs[0].(map[string]any); hm != nil {
			cmd, _ := hm["command"].(string)
			return cmd
		}
	}
	return ""
}
