package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	_ = os.WriteFile(path, []byte("not json {"), 0o644)
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
	_ = os.WriteFile(path, []byte(src), 0o644)
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
	sts := Wire(dir, []string{"mcp", "opencode"}, false, false)
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
	Wire(dir, []string{"mcp", "opencode"}, false, false)
	Wire(dir, []string{"mcp", "opencode"}, false, false)
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
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Claude\n"), 0o644)
	sts := Wire(dir, []string{"mcp", "opencode"}, false, false)
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
	Wire(dir, []string{"mcp", "opencode"}, false, false)
	c2, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if strings.Count(string(c2), "kern usage rules") != 1 {
		t.Fatalf("CLAUDE.md rules duplicated: %s", c2)
	}
}

func TestCheckReports(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no host-installed wrappers leak into the check
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
	sts := Wire(dir, nil, false, true)

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

func TestDocsStateMCPToolCount(t *testing.T) {
	count := 0
	for _, n := range mcp.ToolNames() {
		if strings.HasPrefix(n, "kern_") {
			count++
		}
	}
	if count < 40 {
		t.Fatalf("suspiciously small MCP catalog: %d tools", count)
	}
	want := strconv.Itoa(count)

	// Tests run with the package dir as CWD (internal/setup); docs live at
	// the repo root, two levels up — same pattern as the plugin parity test.
	readRoot := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	readme := readRoot("README.md")
	agents := readRoot("AGENTS.md")

	check := func(label, text, pattern string) {
		re := regexp.MustCompile(pattern)
		if !re.MatchString(text) {
			t.Errorf("%s: does not state the registered catalog size (%s tools) — pattern %q", label, want, pattern)
		}
	}

	check("README banner", readme, `\(11 high-level tools by default, `+want+` in full mode\)`)
	check("README MCP section", readme, `by default, `+want+` in full mode\)`)
	check("README agents section", readme, `by default, `+want+`\nin full mode`)
	check("AGENTS kern_meta section", agents, `among `+want+` individual `+"`kern_\\*`"+` tools`)
	check("AGENTS catalog section", agents, `ships `+want+` `+"`kern_\\*`"+` MCP tools`)
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

// cliTableEntryRe matches a `"name": {run: ...}` entry in the commandTable
// map (cmd/kern/dispatch_table.go) — the E3 dispatch-table refactor fused the
// old `switch cmd` into commandTable, so the parity source of truth moved from
// dispatch.go case labels to table keys.
var cliTableEntryRe = regexp.MustCompile(`(?m)^\s*"([^"]+)":\s*\{run:`)

// toolStartRe finds each kern_xxx tool definition; used to delimit tool bodies.
var toolStartRe = regexp.MustCompile(`kern_[a-zA-Z0-9_]+:\s*tool\(`)

// flagsFirstSubRe matches `const flags: string[] = ["sub", ...]` — the common
// pattern where a tool builds its argument vector before run().
var flagsFirstSubRe = regexp.MustCompile(`const (?:flags|rest): string\[\] = \["([^"]+)"`)

// runFirstSubRe matches `run(["sub", ...])` for tools that dispatch directly.
var runFirstSubRe = regexp.MustCompile(`run\(\["([^"]+)"`)

// runPayloadFirstSubRe matches `runPayload(["sub", ...])` — the report-
// preserving wrapper used by tools whose CLI exits non-zero by design (CI
// signal); the first element is still the top-level subcommand.
var runPayloadFirstSubRe = regexp.MustCompile(`runPayload\(\["([^"]+)"`)

// cliSubcommands returns the set of top-level subcommands handled by the kern
// CLI (the keys of commandTable in cmd/kern/dispatch_table.go).
func cliSubcommands(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "kern", "dispatch_table.go"))
	if err != nil {
		t.Fatalf("read cmd/kern/dispatch_table.go: %v", err)
	}
	set := map[string]bool{}
	for _, m := range cliTableEntryRe.FindAllStringSubmatch(string(b), -1) {
		set[m[1]] = true
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
	_ = json.Unmarshal(b, &m)
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
	_ = os.WriteFile(gpath, b, 0o644)

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
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644)
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

func TestWireLocalGitExclude(t *testing.T) {
	dir := t.TempDir()
	// Test on non-git dir: should fail cleanly
	st := wireLocalGitExclude(dir)
	if st.Installed {
		t.Fatal("expected not installed for non-git dir")
	}

	// Create a simulated .git directory
	gitDir := filepath.Join(dir, ".git")
	_ = os.MkdirAll(filepath.Join(gitDir, "info"), 0o755)

	st = wireLocalGitExclude(dir)
	if !st.Installed {
		t.Fatalf("expected installed, got error: %s", st.Note)
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")
	b, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("failed to read exclude: %v", err)
	}
	if !strings.Contains(string(b), ".kern/") {
		t.Fatalf("expected .kern/ in exclude, got:\n%s", string(b))
	}

	// Idempotent: second run does not duplicate
	before := string(b)
	wireLocalGitExclude(dir)
	b, _ = os.ReadFile(excludePath)
	if string(b) != before {
		t.Fatal("exclude duplicated on re-run")
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

	sts := Wire(dir, nil, true, true)
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

	sts := Wire(dir, nil, true, false)
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

// TestSkillCopiesParity asserts that all copies of each skill playbook across
// .agents/skills, .github/skills, .opencode/skills, and internal/skills/assets
// stay byte-identical.
func TestSkillCopiesParity(t *testing.T) {
	skills := []string{
		"kern-investigate",
		"kern-safe-change",
		"kern-incident-triage",
		"kern-team-orchestration",
	}
	roots := []string{
		filepath.Join("..", "..", ".agents", "skills"),
		filepath.Join("..", "..", ".github", "skills"),
		filepath.Join("..", "..", ".opencode", "skills"),
		filepath.Join("..", "..", "internal", "skills", "assets"),
	}

	for _, s := range skills {
		var canonical []byte
		var canonicalPath string
		for _, r := range roots {
			p := filepath.Join(r, s, "SKILL.md")
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			if canonical == nil {
				canonical = data
				canonicalPath = p
			} else if !bytes.Equal(canonical, data) {
				t.Errorf("%s drifted from %s — run: kern setup to sync", p, canonicalPath)
			}
		}
	}
}

// TestMCPDocumentationCountsParity asserts that tool counts mentioned in
// docs/mcp/* and docs/mcp-client.md match the live registered catalog count.
func TestMCPDocumentationCountsParity(t *testing.T) {
	countStr := strconv.Itoa(len(mcp.ToolNames()))

	docFiles := []string{
		filepath.Join("..", "..", "docs", "mcp-client.md"),
		filepath.Join("..", "..", "docs", "mcp", "README.md"),
		filepath.Join("..", "..", "docs", "mcp", "versioning.md"),
		filepath.Join("..", "..", "docs", "mcp", "protocol.md"),
	}

	for _, f := range docFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(data)
		if !strings.Contains(text, countStr) {
			t.Errorf("%s does not mention live tool count %s", f, countStr)
		}
		// Stale counts must not be present
		for _, stale := range []string{"121", "127"} {
			if strings.Contains(text, stale+" tool") || strings.Contains(text, stale+" `kern_*`") || strings.Contains(text, stale+"-tool") {
				t.Errorf("%s contains stale tool count %s", f, stale)
			}
		}
	}
}

func TestWireScaffoldsKernConfig(t *testing.T) {
	dir := t.TempDir()
	Wire(dir, nil, false, false)

	profilesPath := filepath.Join(dir, ".kern", "profiles.json")
	b, err := os.ReadFile(profilesPath)
	if err != nil {
		t.Fatalf("missing .kern/profiles.json: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("profiles.json = %q, want []", b)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".kern", "skills")); err != nil || !fi.IsDir() {
		t.Fatalf(".kern/skills missing or not a dir: %v", err)
	}

	// An existing profiles.json is never overwritten.
	if err := os.WriteFile(profilesPath, []byte(`[{"name":"mine"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	Wire(dir, nil, false, false)
	b2, _ := os.ReadFile(profilesPath)
	if string(b2) != `[{"name":"mine"}]` {
		t.Fatalf("profiles.json was overwritten: %q", b2)
	}
}

func TestGitignoreGeneratedBlueprintRuntime(t *testing.T) {
	dir := t.TempDir()
	st := gitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range []string{
		".blueprint/audit/",
		".blueprint/receipts/",
		".blueprint/verdict-cache/",
		".blueprint/fingerprint-cache/",
		".blueprint/metrics.json",
		".kern/",
	} {
		if !strings.Contains(content, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, content)
		}
	}
	// No wholesale .blueprint/ ignore and no config-file ignores.
	for _, banned := range []string{
		"\n.blueprint/\n",
		"config.yaml",
		"suppressions.yaml",
		"owners.yaml",
	} {
		if strings.Contains(content, banned) {
			t.Errorf(".gitignore must not contain %q (user config stays committable):\n%s", banned, content)
		}
	}
	// Idempotent re-run.
	before := content
	gitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("gitignore block changed on re-run")
	}
	if got := strings.Count(string(b), ".blueprint/audit/"); got != 1 {
		t.Fatalf("blueprint audit entry appears %d times, want 1", got)
	}
}

func TestGitignoreGeneratedIdempotentReplace(t *testing.T) {
	dir := t.TempDir()
	legacy := "# user section\nfoo/\n" + gitignoreMarker + "\n.old-entry/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	st := gitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(b)
	if !strings.HasPrefix(content, "# user section\nfoo/\n") {
		t.Fatalf("user content not preserved:\n%s", content)
	}
	if got := strings.Count(content, gitignoreMarker); got != 1 {
		t.Fatalf("expected exactly one kern block, got %d markers:\n%s", got, content)
	}
	if strings.Contains(content, ".old-entry/") {
		t.Fatalf("legacy block not replaced (stale entry still present):\n%s", content)
	}
	// Run twice: still one block, byte-identical.
	before := content
	gitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("second run changed the file")
	}
	if got := strings.Count(string(b), gitignoreMarker); got != 1 {
		t.Fatalf("second run duplicated the kern block: %d markers", got)
	}
}

func TestWireProjectScopeSkipsGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()

	// Project-only wiring: no user-global hook files may appear.
	Wire(root, nil, false, false)
	for _, rel := range []string{".claude/settings.json", ".cursor/hooks.json"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
			t.Fatalf("project wiring must not create global file ~/%s", rel)
		}
	}

	// Global wiring: both hook files are created.
	Wire(root, nil, false, true)
	for _, rel := range []string{".claude/settings.json", ".cursor/hooks.json"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Fatalf("--global must create ~/%s: %v", rel, err)
		}
	}
}

func TestWireGlobalGitignoreBlueprintRuntime(t *testing.T) {
	dir := withTempHome(t, true) // XDG_CONFIG_HOME -> dir/.config
	if err := os.MkdirAll(filepath.Join(dir, ".config", "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := wireGlobalGitignore()
	if !st.Installed {
		t.Fatalf("global gitignore failed: %s", st.Note)
	}
	ignorePath := filepath.Join(dir, ".config", "git", "ignore")
	b, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range append([]string{".kern/"}, blueprintRuntimeEntries...) {
		if !strings.Contains(content, want) {
			t.Errorf("global git ignore missing %q:\n%s", want, content)
		}
	}
	for _, banned := range []string{"config.yaml", "suppressions.yaml", "owners.yaml"} {
		if strings.Contains(content, banned) {
			t.Errorf("global git ignore must not contain %q:\n%s", banned, content)
		}
	}
	// Idempotent re-run: nothing added, status says already configured.
	before := content
	st2 := wireGlobalGitignore()
	b, _ = os.ReadFile(ignorePath)
	if string(b) != before {
		t.Fatal("global git ignore changed on re-run")
	}
	if st2.Note != "global git ignore already configured" {
		t.Fatalf("re-run note = %q, want already-configured", st2.Note)
	}
}

// TestWireCopilotWritesMCPConfig verifies the alignment fix: `kern setup
// --agents copilot` must wire BOTH the global preToolUse hook
// (~/.copilot/hooks/kern-pretooluse.json) AND the MCP server config at the
// reference path ~/.copilot/mcp-config.json (mcpServers.kern, command from
// PortableMCPCommand). Previously the global MCP adapter was gated under a
// separate "copilot-cli" name and resolved via XDG (~/.config/.copilot), so an
// explicit --agents copilot run left ~/.copilot with hooks only.
func TestWireCopilotWritesMCPConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config")) // must NOT receive mcp-config.json

	sts := Wire(dir, []string{"copilot"}, false, true)

	// The global MCP config must land directly under HOME, not XDG.
	mcpPath := filepath.Join(dir, ".copilot", "mcp-config.json")
	b, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("copilot mcp-config.json not written at %s (statuses: %+v): %v", mcpPath, sts, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("copilot mcp-config.json not valid JSON: %v\n%s", err, b)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	kern, _ := servers["kern"].(map[string]any)
	if kern == nil {
		t.Fatalf("mcpServers.kern missing from copilot mcp-config.json:\n%s", b)
	}
	if kern["command"] != PortableMCPCommand() {
		t.Errorf("mcpServers.kern command = %v, want %q", kern["command"], PortableMCPCommand())
	}

	// Hooks must still be wired, and the MCP config must NOT go to XDG.
	if _, err := os.Stat(filepath.Join(dir, ".copilot", "hooks", "kern-pretooluse.json")); err != nil {
		t.Errorf("copilot hooks not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", ".copilot", "mcp-config.json")); err == nil {
		t.Error("copilot mcp-config.json must not be written under XDG_CONFIG_HOME")
	}
}

// TestWireQoderWritesMCPSettings verifies the alignment fix: qoder's hook
// config lives in ~/.qoder/settings.json, so the mcpServers entry must land in
// the SAME file (matching qwen's ~/.qwen/settings.json wiring) instead of a
// separate ~/.qoder/mcp.json. After Wire, settings.json must contain both the
// PreToolUse guard hook and mcpServers.kern.
func TestWireQoderWritesMCPSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	sts := Wire(dir, []string{"qoder"}, false, true)

	path := filepath.Join(dir, ".qoder", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("qoder settings.json not written at %s (statuses: %+v): %v", path, sts, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("qoder settings.json not valid JSON: %v\n%s", err, b)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if _, ok := servers["kern"].(map[string]any); !ok {
		t.Fatalf("mcpServers.kern missing from qoder settings.json:\n%s", b)
	}
	hooks, _ := m["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) == 0 {
		t.Fatalf("qoder PreToolUse hook missing from settings.json:\n%s", b)
	}
	if !strings.Contains(string(b), "kern-guard.sh") {
		t.Fatalf("qoder settings.json does not reference kern-guard.sh:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".qoder", "mcp.json")); err == nil {
		t.Error("qoder mcpServers must live in settings.json, not a separate mcp.json")
	}
}
