package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergePrependPreservesOtherContent(t *testing.T) {
	oldKern := "# kern usage rules\n\nold old\n\n"
	other := "# graphify\n\nsome content\n\n# code-review\n\nmore\n"
	merged := mergePrepend(oldKern+other, "# kern usage rules\n\nnew new\n")
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
	first := mergePrepend(existing, kern)
	second := mergePrepend(first, kern)
	if first != second {
		t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(second, "# kern usage rules") != 1 {
		t.Fatalf("kern section duplicated:\n%s", second)
	}
}

func TestMergeAppendPreservesOtherContent(t *testing.T) {
	existing := "# kern usage rules\n\nold\n\n# my rules\n\nkeep\n"
	merged := mergeAppend(existing, "# kern usage rules\n\nnew\n")
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

func TestRemoveKernSection(t *testing.T) {
	if got := removeKernSection("no kern here"); got != "no kern here" {
		t.Fatalf("no-op expected, got %q", got)
	}
	in := "# kern usage rules\n\nold\n\n# next header\n\nbody\n"
	want := "# next header\n\nbody\n"
	if got := removeKernSection(in); got != want {
		t.Fatalf("removeKernSection mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
	// Kern at EOF: remove through end.
	if got := removeKernSection("# kern usage rules\n\nold"); got != "" {
		t.Fatalf("expected empty, got %q", got)
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
		t.Cleanup(func() { os.Setenv("XDG_CONFIG_HOME", oldXDG) })
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
	dir := withTempHome(t, false) // no .claude, no opencode config
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
