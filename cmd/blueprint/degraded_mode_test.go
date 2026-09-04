package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runCheckWithEnv runs `blueprint check --staged` against dir with extra env
// overrides and returns (combined output, exit code). The KERN_BINARY override
// is appended LAST so it wins over the inherited environment.
func runCheckWithEnv(t *testing.T, binPath, dir string, extraArgs, extraEnv []string) (string, int) {
	t.Helper()
	args := append([]string{"check", "--staged", "--repo", dir}, extraArgs...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint check: %v\n%s", err, out)
		}
	}
	return string(out), code
}

// TestCheck_DegradedMode_KernMissing: with KERN_BINARY pointing at a missing
// binary, `blueprint check` must NOT hard-fail (P0.3). It runs in degraded
// mode: the architecture check reports a kern:unavailable WARN finding, the
// audit chain stays local-only, and gitleaks/jscpd still run. A missing kern
// must never yield exit 2 — the pipeline continues and Aggregate computes the
// real verdict (0 for WARN-only, 1 if another check BLOCKs).
func TestCheck_DegradedMode_KernMissing(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a clean change with no secrets: expected verdict is WARN (degraded
	// architecture), exit 0 — never 2.
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "web/web.go")

	// KERN_BINARY=/nonexistent/path is deterministic: resolveKernBinary errors
	// on a bad KERN_BINARY without falling back to PATH.
	out, code := runCheckWithEnv(t, bin, dir, []string{"--format=json"}, []string{"KERN_BINARY=/nonexistent/kern/binary"})
	if code != 0 {
		t.Fatalf("exit=%d want 0 (degraded WARN, never 2); output:\n%s", code, out)
	}
	if !strings.Contains(out, "degraded mode") {
		t.Fatalf("stderr missing 'degraded mode' warning:\n%s", out)
	}
	if !strings.Contains(out, "kern:unavailable") {
		t.Fatalf("output missing kern:unavailable WARN finding:\n%s", out)
	}
	// The architecture leg must be a visible WARN, not a silent skip.
	if !strings.Contains(out, `"status": "WARN"`) {
		t.Fatalf("output missing WARN verdict for degraded architecture:\n%s", out)
	}
}

// TestCheck_RequireKern_HardFails: with --require-kern and a missing kern
// binary, the check preserves the old hard-fail behavior (exit 2) as an
// explicit opt-in.
func TestCheck_RequireKern_HardFails(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "a.go", "package a\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	g4WriteFile(t, dir, "a.go", "package a\nfunc B() {}\n")
	g4RunGit(t, dir, "add", "a.go")

	out, code := runCheckWithEnv(t, bin, dir, []string{"--format=json", "--require-kern"}, []string{"KERN_BINARY=/nonexistent/kern/binary"})
	if code != 2 {
		t.Fatalf("exit=%d want 2 (--require-kern hard-fail); output:\n%s", code, out)
	}
	if !strings.Contains(out, "kern binary not found") {
		t.Fatalf("output missing kern-binary error message:\n%s", out)
	}
}
