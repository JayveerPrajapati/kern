package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeAgentCLI writes a fake agent binary into a temp dir and prepends it to
// PATH. The fake echoes the prompt back wrapped in markers so tests can assert
// the full argv/stdin plumbing. Returns the bin dir and a cleanup func.
func fakeAgentCLI(t *testing.T, name string, behavior string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	switch behavior {
	case "echo":
		// echo the LAST argument (the prompt) regardless of flag count
		script += "for last; do :; done; echo \"[[$last]]\"\n"
	case "fail":
		script += "echo 'boom' >&2\nexit 1\n"
	case "empty":
		script += "exit 0\n"
	case "slow":
		script += "sleep 5\n"
	case "slow30":
		// Absolute /bin/sleep: the test's isolatePATH strips /usr/bin from
		// PATH, so a bare `sleep` would fail with exit 127 instead of
		// blocking. Long enough that the 1s test timeout fires mid-run, and
		// long enough that an orphaned grandchild would still be alive when
		// the test pgreps for it.
		script += "/bin/sleep 30\n"
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolatePATH restricts PATH to the given fake dirs (host agent CLIs like a
// real claude/codex would leak into availability probes otherwise; the fake
// scripts use absolute /bin/sh so a restricted PATH is safe).
func isolatePATH(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func TestLocalCliProviderGenerate(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("claude")
	got, err := p.Generate(context.Background(), "sys", "user text", Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "[[sys\n\nuser text]]" {
		t.Fatalf("got %q, want system+user joined as the single prompt arg", got)
	}
	if cap := p.Capabilities(); !cap.Generate || cap.Embed || cap.Stream {
		t.Fatalf("capabilities = %+v, want Generate-only", cap)
	}
}

func TestLocalCliProviderMissingBinary(t *testing.T) {
	// No fake on PATH for this agent name: it must fail loudly and
	// actionably, not hang or silently no-op.
	p := NewLocalCliProvider("gemini")
	_, err := p.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error should name the missing install: %v", err)
	}
}

func TestLocalCliProviderFailureSurfacesStderr(t *testing.T) {
	d1 := fakeAgentCLI(t, "codex", "fail")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("codex")
	_, err := p.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not surfaced in error: %v", err)
	}
}

func TestLocalCliProviderEmptyOutput(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "empty")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("claude")
	if _, err := p.Generate(context.Background(), "s", "u", Options{}); err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestAvailableLocalAgents(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	d2 := fakeAgentCLI(t, "qwen", "echo")
	isolatePATH(t, d1, d2)
	got := AvailableLocalAgents()
	if len(got) != 2 || got[0] != "claude" || got[1] != "qwen" {
		t.Fatalf("AvailableLocalAgents = %v, want [claude qwen] in preference order", got)
	}
}

func TestChainProviderFallsThrough(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	d2 := fakeAgentCLI(t, "codex", "echo")
	isolatePATH(t, d1, d2)
	chain := NewChainProvider(
		NewLocalCliProvider("gemini"), // missing binary -> fails
		NewLocalCliProvider("claude"), // available -> succeeds
	)
	got, err := chain.Generate(context.Background(), "s", "u", Options{})
	if err != nil {
		t.Fatalf("chain should fall through to claude: %v", err)
	}
	if got != "[[s\n\nu]]" {
		t.Fatalf("chain result = %q, want the claude fake output (system+user)", got)
	}
	if !chain.AllLocal() {
		t.Fatal("chain of local providers must be AllLocal")
	}
}

func TestChainProviderAllFailNamesProviders(t *testing.T) {
	d1 := fakeAgentCLI(t, "codex", "fail")
	isolatePATH(t, d1)
	chain := NewChainProvider(
		NewLocalCliProvider("gemini"), // missing
		NewLocalCliProvider("codex"),  // fails with stderr
	)
	_, err := chain.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected all-fail error")
	}
	if !strings.Contains(err.Error(), "gemini") || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error must name every tried provider: %v", err)
	}
}

func TestChainProviderEmbedFallsToCapableOnly(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	isolatePATH(t, d1)
	chain := NewChainProvider(NewLocalCliProvider("claude"))
	if _, err := chain.Embed(context.Background(), "x"); err == nil {
		t.Fatal("embed must fail cleanly when no provider supports it")
	}
}

// pgrepPath is resolved at package init, before any test's isolatePATH
// replaces PATH with the fake-bin dirs (pgrep lives in /usr/bin, which the
// isolated PATH no longer contains).
var pgrepPath = func() string {
	p, err := exec.LookPath("pgrep")
	if err != nil {
		return ""
	}
	return p
}()

// pgrepExact returns the set of PIDs whose full command line exactly matches
// pattern (pgrep -xf). pgrep exits 1 when nothing matches — that is a normal
// empty result, not an error.
func pgrepExact(t *testing.T, pattern string) map[string]bool {
	t.Helper()
	if pgrepPath == "" {
		t.Skip("pgrep not available on this system")
	}
	out, err := exec.Command(pgrepPath, "-xf", pattern).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return map[string]bool{}
		}
		t.Fatalf("pgrep -xf %q: %v", pattern, err)
	}
	pids := map[string]bool{}
	for _, pid := range strings.Fields(string(out)) {
		pids[pid] = true
	}
	return pids
}

func TestLocalCliProviderTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill semantics are POSIX-only")
	}
	d1 := fakeAgentCLI(t, "opencode", "slow30")
	isolatePATH(t, d1)
	// 1s per-call timeout: the stub sleeps 30s, so the timeout branch (not
	// ctx cancellation) must fire and kill the process group.
	t.Setenv("KERN_LLM_CLI_TIMEOUT", "1")
	p := NewLocalCliProvider("opencode")

	before := pgrepExact(t, "/bin/sleep 30")

	start := time.Now()
	_, err := p.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected timeout error from a 30s-sleeping stub with a 1s timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention the timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Generate took %v, expected the ~1s timeout to bound it", elapsed)
	}

	// The stub's `/bin/sleep 30` runs as a CHILD of the stub shell — i.e. a
	// grandchild of Generate's process. Killing only the direct child would
	// orphan it (reparented to PID 1); the fix kills the whole process
	// group, so no NEW sleep-30 process may survive.
	time.Sleep(200 * time.Millisecond) // let SIGKILL delivery + reaping settle
	after := pgrepExact(t, "/bin/sleep 30")
	for pid := range after {
		if !before[pid] {
			t.Errorf("orphaned grandchild `/bin/sleep 30` (pid %s) survived the timeout kill", pid)
		}
	}
}
