package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFindingsCommandExitContract pins the CLI exit-code contract of every
// findings-producing command (the CI signals, per the plugin exit-code
// contract): risk findings exit 3 (policy family — includes security, which
// DRIFTED from 1 to 3 with the fatalPolicy change), blocking gates exit 1,
// failing builds/scripts exit 1, failing sandboxed exec exits 1. The plugin
// shadows recover stdout on ANY nonzero exit, but CI configs gating on exact
// codes break on silent drift — this test fails the build when it happens.
//
// Contract pinned (verified live 2026-09-24):
//
//	kern changes <dirty>      -> 1   (risk findings, fatalFindings)
//	kern review  <dirty>      -> 1   (risk findings, fatalFindings)
//	kern security <secret>    -> 1   (error findings)
//	kern diff-gate --blocking -> 1   (fixture BLOCKs on the hardcoded secret;
//	                                 the WARN→BLOCK --blocking elevation -> 3 is
//	                                 pinned in TestDiffGateServiceVerdicts)
//	kern verify --types build -> 1   (FAIL verdict on a broken build)
//	kern exec <failing>       -> 1   (script exit propagates)
func TestFindingsCommandExitContract(t *testing.T) {
	if runtime.GOOS == "darwin" && os.Getenv("KERN_SANDBOX_ACTIVE") == "1" {
		t.Skip("cannot nest sandbox-exec inside an active kern sandbox on macOS (inner check/build pipeline); covered by direct runs")
	}
	if testing.Short() {
		t.Skip("integration: builds a binary and runs it against a fixture repo")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Build the binary once (the real main() exit path is what we assert).
	bin := filepath.Join(t.TempDir(), "kern")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build kern: %v (%s)", err, out)
	}

	// Fixture repo: committed base + dirty file with a real secret and
	// unformatted code, plus a separate broken-build file.
	fix := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = fix
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(fix, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fix\n\ngo 1.23\n")
	write("main.go", "package main\n\nvar apiKey = \"sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890ABCDEF\"\n\nfunc main(){\n}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	write("main.go", "package main\n\nvar apiKey2 = \"sk-ant-api03-zyxwvutsrqponmlkjihgfedcba9876543210FEDCBA\"\n\nfunc main(){\n}\n")
	write("bad.go", "package main\n\nfunc broken() {\n\tundefinedSymbol()\n}\n")

	runc := func(env []string, args ...string) int {
		cmd := exec.Command(bin, args...)
		cmd.Dir = fix
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout, cmd.Stderr = nil, nil
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		env  []string
		args []string
		want int
	}{
		{"changes dirty -> 1", nil, []string{"changes", "."}, 1},
		{"review dirty -> 1", nil, []string{"review", "."}, 1},
		{"security secret -> 1", nil, []string{"security", "."}, 1},
		{"diff-gate blocking -> 1", nil, []string{"diff-gate", ".", "--blocking", "--no-tests"}, 1},
		{"verify broken build -> 1", nil, []string{"verify", ".", "--types", "build"}, 1},
		{"exec failing script -> 1", []string{"KERN_ALLOW_EXEC=1", "KERN_TOOLS=kern_exec"}, []string{"exec", "exit 7", "--lang", "bash"}, 1},
		{"exec ok script -> 0", []string{"KERN_ALLOW_EXEC=1", "KERN_TOOLS=kern_exec"}, []string{"exec", "echo hello", "--lang", "bash"}, 0},
	}
	for _, c := range cases {
		if got := runc(c.env, c.args...); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

// TestSecGosecExitContract pins the optional gosec engine's exit contract:
// a HIGH-severity gosec finding is advisory under the default error-only
// lens (exit 0 — Model C: external engines never gate), and only
// KERN_SEC_PROMOTE lifting its rule ID reaches the findings-tier exit 1.
func TestSecGosecExitContract(t *testing.T) {
	if runtime.GOOS == "darwin" && os.Getenv("KERN_SANDBOX_ACTIVE") == "1" {
		t.Skip("cannot nest sandbox-exec inside an active kern sandbox on macOS (inner check/build pipeline); covered by direct runs")
	}
	if testing.Short() {
		t.Skip("integration: builds a binary and runs it against a fixture repo")
	}

	// Build the binary once (the real main() exit path is what we assert).
	bin := filepath.Join(t.TempDir(), "kern")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build kern: %v (%s)", err, out)
	}

	// Fixture repo: clean for the internal scanner (no hardcoded secrets),
	// so the exit code is driven solely by the gosec engine.
	fix := t.TempDir()
	if err := os.WriteFile(filepath.Join(fix, "go.mod"), []byte("module fix\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix, "main.go"), []byte("package main\n\nfunc main(){\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake gosec: emits a HIGH-severity G104 finding on main.go and exits 1
	// (gosec's find-issues contract).
	dir := t.TempDir()
	src := filepath.Join(fix, "main.go")
	body := fmt.Sprintf(`{"Issues":[{"severity":"HIGH","rule_id":"G104","details":"audit","file":%q,"line":"3"}]}`, src)
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gosecBin := filepath.Join(dir, "gosec")
	if err := os.WriteFile(gosecBin, []byte("#!/bin/sh\ncat '"+out+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	runc := func(env []string, args ...string) int {
		cmd := exec.Command(bin, args...)
		cmd.Dir = fix
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout, cmd.Stderr = nil, nil
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		env  []string
		args []string
		want int
	}{
		{"sec gosec HIGH default lens -> 0", []string{"KERN_GOSEC=" + gosecBin}, []string{"sec", "."}, 0},
		{"sec gosec HIGH promoted -> 1", []string{"KERN_GOSEC=" + gosecBin, "KERN_SEC_PROMOTE=G104"}, []string{"sec", "."}, 1},
	}
	for _, c := range cases {
		if got := runc(c.env, c.args...); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

// TestAuthorizeContextExitContract pins the authorize-context exit contract
// (P1.4, 2026-10-03 findings): the CODE returns 3 on denial (policy family,
// like every risk/findings command) while docs/authorized-context.md and the
// generated global rules had drifted to 2. This test pins BOTH halves: the
// behavioral exit code (an unknown agent is denied fail-closed at the
// authentication stage -> exit 3) and the doc row, so neither can drift again.
func TestAuthorizeContextExitContract(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: builds a binary and runs it against a fixture repo")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Doc parity first (cheap, no build): the exit-code table must say 3 = denied.
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "authorized-context.md"))
	if err != nil {
		t.Fatalf("read docs/authorized-context.md: %v", err)
	}
	docS := string(doc)
	if !strings.Contains(docS, "| 3    | denied") {
		t.Errorf("docs/authorized-context.md exit-code table must document 3 = denied (code returns 3, policy family)")
	}
	if strings.Contains(docS, "| 2    | denied") {
		t.Errorf("docs/authorized-context.md still documents 2 = denied - the code returns 3; doc and code must match")
	}

	// Behavioral: build the binary and run a denial through the real exit path.
	bin := filepath.Join(t.TempDir(), "kern")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build kern: %v (%s)", err, out)
	}
	fix := t.TempDir()
	if err := os.WriteFile(filepath.Join(fix, "go.mod"), []byte("module fix\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unknown agent -> fail-closed authentication denial -> exit 3.
	cmd := exec.Command(bin, "authorize-context", "-agent", "ghost", "-task", "t", "-root", fix)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 3 {
		t.Errorf("authorize-context denied (unknown agent): exit %d, want 3", got)
	}
}
