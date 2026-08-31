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

func TestMergeJSONHandlesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.jsonc")
	// JSONC with both line and block comments
	src := `{
  // this is a line comment
  "mcp": {
    /* block comment */
    "existing": true
  },
  "other": "value"
}`
	os.WriteFile(path, []byte(src), 0o644)
	entry := map[string]any{"command": []string{"kern-mcp"}, "type": "local", "enabled": true}
	if err := mergeJSON(path, "mcp", entry); err != nil {
		t.Fatalf("mergeJSON failed on JSONC with comments: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("written file is not valid JSON: %v\n%s", err, b)
	}
	// existing key preserved
	if m["other"] != "value" {
		t.Errorf("other key not preserved: %v", m)
	}
	// kern entry present
	mcp, _ := m["mcp"].(map[string]any)
	if mcp == nil || mcp["kern"] == nil {
		t.Errorf("kern entry not merged: %v", m)
	}
}

func TestWireCreatesProjectFiles(t *testing.T) {
	dir := t.TempDir()
	sts := Wire(dir, []string{"mcp", "opencode"}, false)
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
	Wire(dir, []string{"mcp", "opencode"}, false)
	Wire(dir, []string{"mcp", "opencode"}, false)
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
	sts := Wire(dir, []string{"mcp", "opencode"}, false)
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
	Wire(dir, []string{"mcp", "opencode"}, false)
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
	sts := Wire(dir, nil, false)

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

// --- CLI subcommand parity ---
// The opencode plugin shells out to the kern CLI via run([...]) / a flags
// array whose first element is the top-level subcommand (cmd/kern/dispatch.go's
// `switch cmd`). These regexes recover, for each tool, the subcommand token it
// dispatches. A typo'd subcommand in the plugin would otherwise sail through
// the name-only parity test; here the token must resolve to a real case.

// cliTopLevelCaseRe matches a top-level `case "name"[, "alias"...]:` inside the
// `switch cmd` block. Top-level cases are indented with exactly one tab, which
// excludes the nested sub-dispatch switches (memory, hook, docs, guard, repos,
// semcache).
var cliTopLevelCaseRe = regexp.MustCompile(`(?m)^\tcase\s+([^:]+):`)

// toolStartRe finds each kern_xxx tool definition; used to delimit tool bodies.
var toolStartRe = regexp.MustCompile(`kern_[a-zA-Z0-9_]+:\s*tool\(`)

// flagsFirstSubRe matches `const flags: string[] = ["sub", ...]` — the common
// pattern where a tool builds its argument vector before run().
var flagsFirstSubRe = regexp.MustCompile(`const flags: string\[\] = \["([^"]+)"`)

// runFirstSubRe matches `run(["sub", ...])` for tools that dispatch directly.
var runFirstSubRe = regexp.MustCompile(`run\(\["([^"]+)"`)

// runPayloadFirstSubRe matches `runPayload(["sub", ...])` — the report-
// preserving wrapper used by tools whose CLI exits non-zero by design (CI
// signal); the first element is still the top-level subcommand.
var runPayloadFirstSubRe = regexp.MustCompile(`runPayload\(\["([^"]+)"`)

// cliSubcommands returns the set of top-level subcommands handled by the kern
// CLI (the `case "<name>"` labels of the `switch cmd` in cmd/kern/dispatch.go).
func cliSubcommands(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "kern", "dispatch.go"))
	if err != nil {
		t.Fatalf("read cmd/kern/dispatch.go: %v", err)
	}
	set := map[string]bool{}
	for _, m := range cliTopLevelCaseRe.FindAllStringSubmatch(string(b), -1) {
		// m[1] is e.g. `"version", "--version", "-v"` — collect every label.
		for _, l := range strings.Split(m[1], ",") {
			l = strings.TrimSpace(l)
			if l == "" || len(l) < 3 || l[0] != '"' || l[len(l)-1] != '"' {
				continue
			}
			set[l[1:len(l)-1]] = true
		}
	}
	return set
}

// pluginToolSubcommand returns the CLI subcommand a plugin tool dispatches
// (the first element of its flags/run argument vector), or "" if none could be
// parsed. Parsing a tool body is enough to detect drift without running the
// CLI — a missing/renamed subcommand or a changed dispatch shape fails here.
func pluginToolSubcommand(toolBody string) string {
	if m := flagsFirstSubRe.FindStringSubmatch(toolBody); m != nil {
		return m[1]
	}
	if m := runFirstSubRe.FindStringSubmatch(toolBody); m != nil {
		return m[1]
	}
	if m := runPayloadFirstSubRe.FindStringSubmatch(toolBody); m != nil {
		return m[1]
	}
	return ""
}

// TestPluginSubcommandsReachCLI is the execution-parity half of the plugin
// invariant. It asserts that every kern_xxx tool in the plugin maps to a CLI
// subcommand that actually exists in cmd/kern's dispatch, so a typo'd or
// renamed subcommand (which the name-only TestPluginMatchesMCPCatalog cannot
// see) fails the build. It is static token parsing — deterministic and fast,
// no CLI build or run.
func TestPluginSubcommandsReachCLI(t *testing.T) {
	cli := cliSubcommands(t)
	if len(cli) < 20 {
		t.Fatalf("suspiciously small CLI subcommand set: %d", len(cli))
	}

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

	// Map each tool name to the subcommand it dispatches, so a tool that is
	// silently dropped from the plugin (or whose dispatch is ambiguous) is
	// reported with its name.
	checked := 0
	for _, src := range []string{"embedded", "repo"} {
		content, err := readSrc(src)
		if err != nil {
			t.Fatalf("read %s plugin: %v", src, err)
		}
		idx := toolStartRe.FindAllStringIndex(content, -1)
		for i, loc := range idx {
			start := loc[1]
			end := len(content)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			body := content[start:end]
			name := content[loc[0]:loc[1]]
			name = strings.TrimSuffix(name, ": tool(")
			sub := pluginToolSubcommand(body)
			if sub == "" {
				t.Errorf("%s: %s: could not parse CLI subcommand from tool body", src, name)
				continue
			}
			checked++
			if !cli[sub] {
				t.Errorf("%s: %s dispatches to subcommand %q which does not exist in cmd/kern/dispatch.go (typo or stale mapping)", src, name, sub)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no plugin tools parsed — parity check did not run")
	}
}

func TestWireClaudeHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // claude hooks are now global (user-scope)
	st := wireClaudeHooks("/x/kern")
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
	if st := wireClaudeHooks("/x/kern"); !st.Installed {
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
	t.Setenv("HOME", dir) // gemini hooks are now global (user-scope)
	// A pre-existing ~/.gemini/settings.json with mcpServers (as the adapter
	// writer produces) must keep that key when hooks are merged in.
	gpath := filepath.Join(dir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(gpath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{"mcpServers": map[string]any{"kern": map[string]any{"type": "stdio"}}}
	b, _ := json.Marshal(existing)
	os.WriteFile(gpath, b, 0o644)

	st := wireGeminiHooks("/x/kern")
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

func TestDetectAgents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// No agents present — expect empty (or minimal) detection
	detected := DetectAgents(dir)
	for _, d := range detected {
		t.Logf("detected (empty root): %s", d)
	}

	// Simulate a CLAUDE.md and cursor config to trigger detection
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# project"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cursor", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	detected = DetectAgents(dir)
	has := map[string]bool{}
	for _, d := range detected {
		has[d] = true
	}
	if !has["claude"] {
		t.Errorf("expected claude in detected: %v", detected)
	}
	if !has["cursor"] {
		t.Errorf("expected cursor in detected: %v", detected)
	}
}

// TestWireDetectEmptyPreWiresGlobalOnly verifies the global-first semantics:
// when --detect finds NO agents, no per-repo agent files are written, but
// global-scoped configs (hooks + home/global MCP adapters) are still pre-wired
// for ALL agents, so an agent installed later is already wired with no re-run.
// The universal per-repo files (.mcp.json, AGENTS.md, .gitignore) are always
// written; per-repo agent files (CLAUDE.md instruction, .cursor/rules, .vscode
// adapters) are not.
func TestWireDetectEmptyPreWiresGlobalOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()

	sts := Wire(dir, nil, true)
	var sawGlobalHook, sawUniversal bool
	for _, s := range sts {
		switch s.Agent {
		case "cursor-hooks", "gemini-hooks", "claude-hooks", "codex-hooks", "copilot-hooks", "qwen-hooks", "qoder-hooks":
			sawGlobalHook = true
			if !s.Installed {
				t.Fatalf("global hook %s must be pre-wired, got: %+v", s.Agent, s)
			}
		case "mcp", "AGENTS.md", "gitignore":
			sawUniversal = true
		}
		if !s.Installed && !s.Skipped {
			t.Fatalf("no status may be a real failure in detect-empty mode, got: %+v", s)
		}
	}
	if !sawGlobalHook {
		t.Fatalf("expected global hooks pre-wired for all agents, got: %+v", sts)
	}
	if !sawUniversal {
		t.Fatalf("expected universal per-repo files written, got: %+v", sts)
	}
	// Per-repo agent files must NOT be created: no instruction files, no
	// .cursor/rules, no .vscode adapters, no claude/gemini per-repo configs.
	for _, rel := range []string{
		"CLAUDE.md", "GEMINI.md", ".github/copilot-instructions.md",
		".cursor/rules", ".vscode/mcp.json", ".claude/settings.json", ".gemini/settings.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("detect-empty must not create per-repo agent file %s", rel)
		}
	}
}

func TestWireDetectWiresInstructions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Simulate agent presence
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# project"), 0o644); err != nil {
		t.Fatal(err)
	}

	sts := Wire(dir, nil, true)
	found := false
	for _, s := range sts {
		if s.Agent == "claude-instruction" && s.Installed {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected claude-instruction to be wired: %+v", sts)
	}
	// Verify content
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "kern usage rules") {
		t.Errorf("CLAUDE.md missing kern-first policy")
	}
}

// TestAGENTSMdParity is the AGENTS.md analog of the plugin parity invariant:
// the embedded asset (what `kern setup` installs everywhere) must be
// byte-identical to the repo's own root AGENTS.md. A fix applied to the
// working copy but not synced would ship to every user while the repo itself
// reads stale instructions — and vice versa. Sync with:
//
//	cp AGENTS.md internal/setup/assets/AGENTS.md
func TestAGENTSMdParity(t *testing.T) {
	emb, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		t.Fatalf("read embedded AGENTS.md: %v", err)
	}
	repo, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read repo AGENTS.md: %v", err)
	}
	if !bytes.Equal(emb, repo) {
		t.Error("internal/setup/assets/AGENTS.md drifted from AGENTS.md — run: cp AGENTS.md internal/setup/assets/AGENTS.md")
	}
}
