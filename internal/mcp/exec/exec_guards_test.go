package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSplitShellLine pins the quote/escape-aware splitter that decides argv for
// the guarded command runner: a naive strings.Fields would break any quoted
// argument (and would have turned a quoted flag value into two argv entries).
func TestSplitShellLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"echo hi", []string{"echo", "hi"}},
		{"  go   test\t./...  ", []string{"go", "test", "./..."}},
		{`grep -n "two words" file`, []string{"grep", "-n", "two words", "file"}},
		{`echo 'single quoted'`, []string{"echo", "single quoted"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		{`echo "a\"b"`, []string{"echo", `a"b`}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitShellLine(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %q want %q", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q: got %q want %q", c.in, got, c.want)
			}
		}
	}
}

// TestParseTimeoutSecs pins the shared timeout contract: negatives are a loud
// error (they used to be silently ignored), zero keeps the default.
func TestParseTimeoutSecs(t *testing.T) {
	if sec, err := parseTimeoutSecs("30"); err != nil || sec != 30 {
		t.Fatalf("30: %d %v", sec, err)
	}
	if sec, err := parseTimeoutSecs("0"); err != nil || sec != 0 {
		t.Fatalf("0: %d %v", sec, err)
	}
	if _, err := parseTimeoutSecs("-5"); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative must error, got %v", err)
	}
	if _, err := parseTimeoutSecs("soon"); err == nil {
		t.Fatal("non-numeric must error")
	}
}

// TestScriptTimeoutCap pins the ceiling logic: zero falls back to 10s, a huge
// request is clamped by the cap (env-overridable), and a normal request passes
// through.
func TestScriptTimeoutCap(t *testing.T) {
	if got := scriptTimeout(0); got != 10*time.Second {
		t.Fatalf("zero should yield 10s, got %s", got)
	}
	if got := scriptTimeout(7); got != 7*time.Second {
		t.Fatalf("7 should pass through, got %s", got)
	}
	t.Setenv("KERN_EXEC_MAX_TIMEOUT_SEC", "12")
	if got := scriptTimeout(600); got != 12*time.Second {
		t.Fatalf("cap not applied: %s", got)
	}
	// An unparseable cap keeps the built-in ceiling rather than disabling it.
	t.Setenv("KERN_EXEC_MAX_TIMEOUT_SEC", "not-a-number")
	if got := execScriptTimeoutCap(); got != maxExecScriptTimeout {
		t.Fatalf("invalid cap should fall back, got %s", got)
	}
}

// TestMaskedOutputMasksBeforeTruncating pins the security invariant: secrets
// are masked on the FULL output, so a secret straddling the cut is still
// redacted (masking after truncation would leak its tail).
func TestMaskedOutputMasksBeforeTruncating(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	out := maskedOutput("head "+secret+" tail", 4000)
	if strings.Contains(out, secret) {
		t.Fatalf("secret not masked: %q", out)
	}
	long := strings.Repeat("x", 100) + secret
	got := maskedOutput(long, 50)
	if len(got) > 120 || !strings.Contains(got, "truncated") {
		t.Fatalf("truncation missing: %q", got)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("secret leaked through truncation: %q", got)
	}
}

// TestSandboxValidatesBeforeExecuting pins the guards that must fire before any
// process starts: empty command, and a fail-closed governance denial.
func TestSandboxValidatesBeforeExecuting(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	root := t.TempDir()

	if _, err := Sandbox(context.Background(), "id", map[string]any{"root": root}); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("empty command: %v", err)
	}
	if _, err := Sandbox(context.Background(), "id", map[string]any{"root": root, "command": "echo hi"}); err == nil {
		t.Fatal("governance must deny exec without KERN_ALLOW_EXEC/KERN_TOOLS")
	}
	// An open command with a malformed timeout is rejected at parse time, still
	// before execution.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	if _, err := Sandbox(context.Background(), "id", map[string]any{"root": root, "command": "echo hi", "timeout": "-1"}); err == nil {
		t.Fatal("negative timeout must error")
	}
}

// TestExecValidatesArgs pins the kern_exec argument guards.
func TestExecValidatesArgs(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	if _, err := Exec(context.Background(), map[string]any{}); err == nil {
		t.Fatal("empty code must be denied or rejected")
	}

	// Shaped-call mode reads a kept run: an unknown anchor is an error, never an
	// empty success.
	if _, err := Exec(context.Background(), map[string]any{"output": "tail:5", "anchor": "anchor-deadbeef1234"}); err == nil {
		t.Fatal("unknown anchor must error")
	}

	// With exec permitted, invalid numeric arguments are still rejected before
	// anything runs.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	if _, err := Exec(context.Background(), map[string]any{"code": "print(1)", "max": "lots"}); err == nil {
		t.Fatal("non-numeric max must error")
	}
	if _, err := Exec(context.Background(), map[string]any{"code": "print(1)", "timeout": "-3"}); err == nil {
		t.Fatal("negative timeout must error")
	}
}

// TestDiffFiles pins the diff tool: required args, identical files, a real
// diff, and the compact (index-annotated) path with and without a loader.
func TestDiffFiles(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	if err := os.WriteFile(a, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("one\nTWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := DiffFiles(context.Background(), Hooks{}, map[string]any{"a": "x"}); err == nil {
		t.Fatal("a and b are required")
	}
	out, err := DiffFiles(context.Background(), Hooks{}, map[string]any{"root": root, "a": "a.txt", "b": "a.txt"})
	if err != nil || out != "files identical" {
		t.Fatalf("identical files: %q %v", out, err)
	}
	out, err = DiffFiles(context.Background(), Hooks{}, map[string]any{"root": root, "a": "a.txt", "b": "b.txt"})
	if err != nil || !strings.Contains(out, "-two") || !strings.Contains(out, "+TWO") {
		t.Fatalf("unified diff: %q %v", out, err)
	}
	// Compact mode with no index loader still returns a diff (annotations off).
	out, err = DiffFiles(context.Background(), Hooks{}, map[string]any{"root": root, "a": "a.txt", "b": "b.txt", "compact": true})
	if err != nil || !strings.Contains(out, "TWO") {
		t.Fatalf("compact diff: %q %v", out, err)
	}
	// Missing file is an error naming the file.
	if _, err := DiffFiles(context.Background(), Hooks{}, map[string]any{"root": root, "a": "a.txt", "b": "nope.txt"}); err == nil {
		t.Fatal("missing file must error")
	}
}

// TestRootedPathConfinement pins that a relative path is rooted at the project
// and an escaping path is refused.
func TestRootedPathConfinement(t *testing.T) {
	root := t.TempDir()
	got, err := rootedPath(root, "sub/file.txt")
	if err != nil || !strings.HasPrefix(got, root) {
		t.Fatalf("relative path: %q %v", got, err)
	}
	if _, err := rootedPath(root, "../../etc/passwd"); err == nil {
		t.Fatal("escaping path must be refused")
	}
}

// TestValidateGuards pins the validation tool's pre-execution guards: the
// command is subject to the same governance firewall as exec, so it fails
// closed in a default environment.
func TestValidateGuards(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(context.Background(), map[string]any{"root": root}); err == nil {
		t.Fatal("validate must fail closed without exec permission")
	}
	if _, err := Validate(context.Background(), map[string]any{"root": root, "command": "true"}); err == nil {
		t.Fatal("validate with an explicit command must still fail closed")
	}
	if _, err := Validate(context.Background(), map[string]any{"root": root, "command": "true", "timeout": "-2"}); err == nil {
		t.Fatal("negative timeout must error")
	}
}

// TestHealGuards pins the heal tool's argument guards and its playbook helpers
// (pure functions, no LLM involved).
func TestHealGuards(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Default environment: the governance firewall denies before any round.
	if _, err := Heal(context.Background(), map[string]any{"root": root}); err == nil {
		t.Fatal("heal must fail closed without exec permission")
	}
	// With exec permitted, argument validation still happens before the loop.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	if _, err := Heal(context.Background(), map[string]any{"root": root, "max_rounds": "many"}); err == nil || !strings.Contains(err.Error(), "max_rounds") {
		t.Fatalf("non-numeric max_rounds must error, got %v", err)
	}
	if got := truncateMCP(strings.Repeat("y", 100), 10); len(got) > 40 {
		t.Fatalf("truncateMCP did not truncate: %q", got)
	}
	if got := truncateMCP("short", 10); got != "short" {
		t.Fatalf("truncateMCP changed a short string: %q", got)
	}
	if pb := healPlaybook(root); pb == nil {
		t.Fatal("healPlaybook must return a usable store")
	}
}
