package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
//	kern changes <dirty>      -> 3   (risk findings, fatalPolicy)
//	kern review  <dirty>      -> 3   (risk findings, fatalPolicy)
//	kern security <secret>    -> 3   (error findings; was 1 pre-policy-family)
//	kern diff-gate --blocking -> 1   (WARN elevated to BLOCK)
//	kern verify --types build -> 1   (FAIL verdict on a broken build)
//	kern exec <failing>       -> 1   (script exit propagates)
func TestFindingsCommandExitContract(t *testing.T) {
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
		{"changes dirty -> 3", nil, []string{"changes", "."}, 3},
		{"review dirty -> 3", nil, []string{"review", "."}, 3},
		{"security secret -> 3", nil, []string{"security", "."}, 3},
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
