package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

func TestMergePrependPreservesOtherContent(t *testing.T) {
	oldKern := "# kern usage rules\n\nold old\n\n"
	other := "# graphify\n\nsome content\n\n# code-review\n\nmore\n"
	merged, err := mergePrepend(oldKern+other, "# kern usage rules\n\nnew new\n")
	if err != nil {
		t.Fatalf("mergePrepend error: %v", err)
	}
	if strings.Contains(merged, "old old") {
		t.Fatalf("old kern content not removed:\n%s", merged)
	}
	if !strings.Contains(merged, "new new") {
		t.Fatalf("new kern content not prepended:\n%s", merged)
	}
	for _, want := range []string{"# graphify", "some content", "# code-review", "more"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("other content %q not preserved:\n%s", want, merged)
		}
	}
}

func TestMergePrependIdempotent(t *testing.T) {
	kern := "# kern usage rules\n\nnew new\n"
	existing := "# graphify\n\nkeep me\n"
	first, err := mergePrepend(existing, kern)
	if err != nil {
		t.Fatalf("mergePrepend error: %v", err)
	}
	second, err := mergePrepend(first, kern)
	if err != nil {
		t.Fatalf("mergePrepend error: %v", err)
	}
	if first != second {
		t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(second, "# kern usage rules") != 1 {
		t.Fatalf("kern section duplicated:\n%s", second)
	}
}

func TestMergeAppendPreservesOtherContent(t *testing.T) {
	existing := "# kern usage rules\n\nold\n\n# my rules\n\nkeep\n"
	merged, err := mergeAppend(existing, "# kern usage rules\n\nnew\n")
	if err != nil {
		t.Fatalf("mergeAppend error: %v", err)
	}
	if strings.Contains(merged, "old\n") {
		t.Fatalf("old kern content not removed:\n%s", merged)
	}
	if !strings.Contains(merged, "# my rules") || !strings.Contains(merged, "keep") {
		t.Fatalf("other content lost:\n%s", merged)
	}
	// kern appended at the end.
	if !strings.HasSuffix(merged, "new\n") {
		t.Fatalf("kern not appended at end:\n%s", merged)
	}
}

// TestMergeAppendConvergesMultipleBlocks pins the F15 fix at the writer
// level: repeated `kern setup --global` runs used to accumulate one
// "# kern usage rules" block per run. mergeAppend across three accumulated
// blocks must converge to a single fresh block while keeping user content.
func TestMergeAppendConvergesMultipleBlocks(t *testing.T) {
	in := "# kern usage rules\n\noldest\n\n# user notes\n\nkeep me\n\n# kern usage rules\n\nmiddle\n\n# more user\n\nstill here\n\n# kern usage rules\n\nnewest\n"
	merged, err := mergeAppend(in, "# kern usage rules\n\nfresh\n")
	if err != nil {
		t.Fatalf("mergeAppend error: %v", err)
	}
	if strings.Count(merged, "# kern usage rules") != 1 {
		t.Fatalf("mergeAppend did not converge to a single kern block:\n%s", merged)
	}
	if !strings.Contains(merged, "fresh") || !strings.Contains(merged, "keep me") {
		t.Fatalf("mergeAppend lost fresh or user content:\n%s", merged)
	}
}

// TestMergeCrossStripsMarkedBlock pins the F15 cross-format fix: writeGlobal
// Claude/AGENTS (unmarked "# kern usage rules" blocks) and `kern setup
// --global-rules` (marker-delimited block) target the same global files, so
// each writer must strip the OTHER format or the file stacks two coexisting
// kern sections.
func TestMergeCrossStripsMarkedBlock(t *testing.T) {
	marked := globalRulesMarkerOpen + "\n# kern usage rules\n\nmarked\n" + globalRulesMarkerClose + "\n"
	unmarked := "# kern usage rules\n\nunmarked\n"
	existing := "# user header\n\n" + unmarked + marked + "# user footer\n"

	// The marked-block writer path (wireGlobalRulesFile) must drop unmarked blocks.
	cleaned := strutil.RemoveMarkedBlock(existing, globalRulesMarkerOpen, globalRulesMarkerClose)
	cleaned, err := removeKernSection(cleaned)
	if err != nil {
		t.Fatalf("removeKernSection error: %v", err)
	}
	if strings.Contains(cleaned, "unmarked") || strings.Contains(cleaned, globalRulesMarkerOpen) {
		t.Fatalf("marked-block writer left the other format behind:\n%s", cleaned)
	}
	for _, want := range []string{"# user header", "# user footer"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user content %q lost in marked-block path:\n%s", want, cleaned)
		}
	}

	// The unmarked writer paths (writeGlobalClaude/writeGlobalAGENTS) must drop marked blocks.
	unmarkedPath, err := mergeAppend(existing, "# kern usage rules\n\nfresh\n")
	if err != nil {
		t.Fatalf("mergeAppend error: %v", err)
	}
	if strings.Contains(unmarkedPath, globalRulesMarkerOpen) || strings.Count(unmarkedPath, "# kern usage rules") != 1 {
		t.Fatalf("unmarked writer left the marked block behind:\n%s", unmarkedPath)
	}
	prependPath, err := mergePrepend(existing, "# kern usage rules\n\nfresh\n")
	if err != nil {
		t.Fatalf("mergePrepend error: %v", err)
	}
	if strings.Contains(prependPath, globalRulesMarkerOpen) || strings.Count(prependPath, "# kern usage rules") != 1 {
		t.Fatalf("prepend writer left the marked block behind:\n%s", prependPath)
	}
}

// TestWireGlobalPreservesTrailingUserContentNoH1 is the end-to-end re-wire
// repro of the F1 follow-up at the writer level: a first WireGlobal run
// writes the block above bare user content (no H1), and a second run must
// preserve that content byte-for-byte and report the file as already
// current (no rewrite).
func TestWireGlobalPreservesTrailingUserContentNoH1(t *testing.T) {
	dir := withTempHome(t, true)
	notes := "my personal notes\n- remember milk\n- fix the fence\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := WireGlobal(nil); !st[0].Installed {
		t.Fatalf("first wire: global AGENTS.md should be written, got: %+v", st[0])
	}
	first, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "my personal notes") {
		t.Fatalf("first wire lost the user content:\n%s", first)
	}
	// Re-wire: content must be preserved AND the file must be a no-op (the
	// writer reports "already present" with Installed=true — the file was
	// left byte-identical, which is the property that matters).
	st := WireGlobal(nil)
	if !strings.Contains(st[0].Note, "already present") {
		t.Fatalf("re-wire note should say 'already present', got: %+v", st[0])
	}
	second, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatalf("re-wire changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestWireGlobalUnbalancedMarkerLeavesFile pins the fail-loud contract at
// the writer level: an unbalanced marker block must surface as a Status
// error and leave the file byte-identical.
func TestWireGlobalUnbalancedMarkerLeavesFile(t *testing.T) {
	dir := withTempHome(t, true)
	broken := "# user header\n\n" + globalRulesMarkerOpen + "\nno close marker\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	st := WireGlobal(nil)
	if st[0].Installed {
		t.Fatalf("unbalanced marker: expected failure status, got: %+v", st[0])
	}
	if !strings.Contains(st[0].Note, "unbalanced") {
		t.Fatalf("unbalanced marker note should say so, got: %+v", st[0])
	}
	b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != broken {
		t.Fatalf("file was modified on error:\ngot:\n%s\nwant:\n%s", b, broken)
	}
}

// withTempHome points the global home resolution at a temp dir for the duration
// of a test, so global wiring never touches the real home.
func withTempHome(t *testing.T, xdg bool) string {
	t.Helper()
	dir := t.TempDir()
	oldHome := globalHomeDir
	globalHomeDir = func() string { return dir }
	t.Cleanup(func() { globalHomeDir = oldHome })
	if xdg {
		oldXDG := os.Getenv("XDG_CONFIG_HOME")
		if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Setenv("XDG_CONFIG_HOME", oldXDG) })
	}
	return dir
}

func TestWireGlobalUsesTempHome(t *testing.T) {
	dir := withTempHome(t, true)
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing AGENTS.md with an old kern section + other content.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# kern usage rules\n\nold kern\n\n# graphify\n\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "CLAUDE.md"), []byte("# claude stuff\n\nkeep claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	WireGlobal(nil)

	ag, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ag), "old kern") {
		t.Fatalf("old kern not removed:\n%s", ag)
	}
	if !strings.Contains(string(ag), "# graphify") {
		t.Fatalf("graphify content lost:\n%s", ag)
	}
	if !strings.Contains(string(ag), "keep") {
		t.Fatalf("other AGENTS.md content lost:\n%s", ag)
	}
	if !strings.HasPrefix(string(ag), "# kern usage rules") {
		t.Fatalf("kern not prepended to AGENTS.md:\n%s", ag)
	}

	cl, err := os.ReadFile(filepath.Join(dir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cl), "keep claude") {
		t.Fatalf("claude content lost:\n%s", cl)
	}
	if !strings.Contains(string(cl), "# kern usage rules") {
		t.Fatalf("kern not appended to claude CLAUDE.md:\n%s", cl)
	}

	if _, err := os.Stat(filepath.Join(dir, ".config", "opencode", "plugins", "kern.ts")); err != nil {
		t.Fatalf("global plugin not copied: %v", err)
	}
}

func TestWireGlobalIdempotent(t *testing.T) {
	dir := withTempHome(t, true)
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# graphify\n\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	WireGlobal(nil)
	first, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	WireGlobal(nil)
	second, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("WireGlobal not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(string(second), "# kern usage rules") != 1 {
		t.Fatalf("kern section duplicated:\n%s", second)
	}
}

func TestWireGlobalSkipsWhenNotInstalled(t *testing.T) {
	// Isolate both the home dir AND XDG_CONFIG_HOME: globalPluginPath reads
	// XDG_CONFIG_HOME first, so without controlling it the ambient value
	// (e.g. /home/runner/.config in CI, where a kern.ts may already exist)
	// would make the plugin "installed" and skip the assertion below.
	dir := withTempHome(t, true) // no .claude, no opencode config
	st := WireGlobal(nil)
	// The universal ~/AGENTS.md is always written.
	if !st[0].Installed {
		t.Fatalf("expected global AGENTS.md written, got: %+v", st[0])
	}
	var sawClaudeSkip, sawPluginSkip bool
	for _, s := range st {
		if s.Agent == "claude-global" && strings.Contains(s.Note, "skipped") {
			sawClaudeSkip = true
		}
		if s.Agent == "opencode-plugin-global" && strings.Contains(s.Note, "skipped") {
			sawPluginSkip = true
		}
	}
	if !sawClaudeSkip {
		t.Fatalf("expected claude skip, got: %+v", st)
	}
	if !sawPluginSkip {
		t.Fatalf("expected plugin skip, got: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "CLAUDE.md")); err == nil {
		t.Fatalf("claude file should not have been created when not installed")
	}
}

// TestGlobalMCPCommandPrefersPATH verifies that the global MCP command resolves
// to a stable PATH-based "kern-mcp" rather than a volatile absolute path, so a
// global agent config survives an upgrade or a change of install location. When
// kern-mcp is resolvable on PATH the command must be the bare "kern-mcp" (the
// agent re-resolves it at launch against whatever kern is currently installed),
// and it must never be an os.Executable()-derived temp/versioned path.
func TestGlobalMCPCommandPrefersPATH(t *testing.T) {
	cmd := GlobalMCPCommand()
	if cmd == "" {
		t.Fatal("GlobalMCPCommand returned empty")
	}
	if strings.Contains(cmd, string(filepath.Separator)) {
		t.Fatalf("GlobalMCPCommand returned an absolute path %q; global configs must use a PATH-resolved bare command so upgrades/relocations don't break MCP", cmd)
	}
	if cmd != "kern-mcp" {
		t.Fatalf("GlobalMCPCommand = %q, want bare \"kern-mcp\" (PATH-resolved at agent launch)", cmd)
	}
}

// TestPortableCLICommandPrefersPATH verifies that the portable CLI command used
// for agent hooks resolves to a stable PATH-based "kern" rather than a volatile
// absolute path, so a hook survives an upgrade or a change of install location.
// When kern is resolvable on PATH the command must be the bare "kern" (the agent
// re-resolves it at launch against whatever kern is currently installed), and it
// must never be an os.Executable()-derived temp/versioned path.
func TestPortableCLICommandPrefersPATH(t *testing.T) {
	cmd := PortableCLICommand()
	if cmd == "" {
		t.Fatal("PortableCLICommand returned empty")
	}
	if strings.Contains(cmd, string(filepath.Separator)) {
		t.Fatalf("PortableCLICommand returned an absolute path %q; hooks must use a PATH-resolved bare command so upgrades/relocations don't break", cmd)
	}
	if cmd != "kern" {
		t.Fatalf("PortableCLICommand = %q, want bare \"kern\" (PATH-resolved at agent launch)", cmd)
	}
}

// TestWireUsesPortableMCPCommand verifies that the project-level configs written
// by Wire reference the kern MCP server via a portable command (bare "kern-mcp"
// or a relative path), never an absolute os.Executable()-derived path, so the
// generated configs work on any machine and survive a kern relocation.
func TestWireUsesPortableMCPCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	Wire(dir, []string{"mcp", "opencode"}, false, false)

	exeDir := ""
	if abs, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(abs)
	}

	for _, name := range []string{"opencode.json", ".mcp.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(b)
		if exeDir != "" && strings.Contains(content, exeDir) {
			t.Errorf("%s contains an absolute os.Executable()-derived path %q (must be portable):\n%s", name, exeDir, content)
		}
	}
}
