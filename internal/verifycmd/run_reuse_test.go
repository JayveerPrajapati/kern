package verifycmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reuseGit runs git with the machine's global hooks disabled (the
// testfixture convention) inside dir.
func reuseGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-c", "core.hooksPath=", "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newReuseRepo returns a committed git repo. The verify command used by
// these tests is `git log --oneline` (allowlisted read-only git): a `go`
// child would fork the toolchain's telemetry subprocess, which writes
// counter files into the redirected HOME AFTER the command returns — racing
// t.TempDir cleanup with "directory not empty" (observed 2026-11-19). HOME
// is redirected so the manifest secret lives in temp space (the
// DangerousClassGated convention).
func newReuseRepo(t testing.TB) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KERN_TOOLS", "kern_verify")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reuseGit(t, dir, "init", "-q")
	reuseGit(t, dir, "add", ".")
	reuseGit(t, dir, "commit", "-qm", "init")
	return dir
}

// TestVerifyCommandReusesIdenticalRun pins the happy path: the same command
// in an unchanged repo is served from the kept run, marked [reused], with
// the original exit code and anchor (which still re-slices).
func TestVerifyCommandReusesIdenticalRun(t *testing.T) {
	dir := newReuseRepo(t)
	first, exit, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil || exit != 0 {
		t.Fatalf("first run: exit=%d err=%v", exit, err)
	}
	if strings.Contains(first, "[reused]") {
		t.Fatalf("first run must be fresh:\n%s", first)
	}
	start := time.Now()
	second, exit, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil || exit != 0 {
		t.Fatalf("second run: exit=%d err=%v", exit, err)
	}
	if !strings.Contains(second, "[reused]") {
		t.Fatalf("second run must be served from the kept run:\n%s", second)
	}
	if !strings.Contains(second, "exit=0") {
		t.Fatalf("reused run must keep the original verdict:\n%s", second)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatalf("reuse lookup took %s — git fingerprint path is too slow", time.Since(start))
	}
	if !strings.Contains(second, "init") {
		t.Fatalf("reused output must be the real command output:\n%s", second)
	}
}

// TestVerifyCommandReuseInvalidatedByTreeChange pins the "never reuse across
// changed environments" invariant: touching a tracked file forces a fresh run.
func TestVerifyCommandReuseInvalidatedByTreeChange(t *testing.T) {
	dir := newReuseRepo(t)
	if _, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"}); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "x.go")
	if err := os.WriteFile(f, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, exit, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil || exit != 0 {
		t.Fatalf("fresh run after tree change: exit=%d err=%v", exit, err)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatal("a changed tree must never serve a reused run")
	}
}

// TestVerifyCommandReuseDisabledByEnv pins the kill-switch.
func TestVerifyCommandReuseDisabledByEnv(t *testing.T) {
	dir := newReuseRepo(t)
	if _, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_VERIFY_REUSE", "0")
	second, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatalf("KERN_VERIFY_REUSE=0 must force a fresh run:\n%s", second)
	}
}

// TestVerifyCommandNeverReusesFailedRuns pins the invariant: a failing
// command is never saved reusable.
func TestVerifyCommandNeverReusesFailedRuns(t *testing.T) {
	dir := newReuseRepo(t)
	if _, exit, _ := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline HEAD~999"}); exit == 0 {
		t.Fatal("unknown revision should fail")
	}
	second, exit, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline HEAD~999"})
	if err != nil {
		t.Fatal(err)
	}
	if exit == 0 {
		t.Fatalf("failing command must keep failing (exit=%d)", exit)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatalf("failed runs must never be reused:\n%s", second)
	}
}

// TestVerifyCommandReuseTamperDetection pins the signed-manifest invariant:
// an edited manifest (command or exit flipped) fails verification and never
// serves.
func TestVerifyCommandReuseTamperDetection(t *testing.T) {
	dir := newReuseRepo(t)
	if _, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(runsDir(dir))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), manifestSuffix) {
			continue
		}
		p := filepath.Join(runsDir(dir), e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		tampered := strings.Replace(string(b), `"exit":0`, `"exit":1`, 1)
		if tampered == string(b) {
			// JSON marshaling of int has no space; try unquoted key form.
			tampered = strings.Replace(string(b), `"command":"git log --oneline"`, `"command":"go build ./..."`, 1)
		}
		if tampered == string(b) {
			t.Fatalf("could not tamper with manifest %s:\n%s", e.Name(), b)
		}
		if err := os.WriteFile(p, []byte(tampered), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	second, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatalf("a tampered manifest must never be served:\n%s", second)
	}
}

// TestVerifyCommandReuseNotAcrossRoots pins root binding: a manifest saved
// in one repo is not served for the same command in another.
func TestVerifyCommandReuseNotAcrossRoots(t *testing.T) {
	dir := newReuseRepo(t)
	if _, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"}); err != nil {
		t.Fatal(err)
	}
	other := newReuseRepo(t)
	second, _, err := VerifyCommand(context.Background(), map[string]any{"root": other, "command": "git log --oneline"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatalf("reuse must never cross roots:\n%s", second)
	}
}

// TestVerifyCommandReuseRespectsMaxAge pins the age bound: an expired
// manifest forces a fresh run (the env knob shortens the window).
func TestVerifyCommandReuseRespectsMaxAge(t *testing.T) {
	dir := newReuseRepo(t)
	if _, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_VERIFY_REUSE_MAX_AGE", "1")
	time.Sleep(1100 * time.Millisecond)
	second, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "git log --oneline"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second, "[reused]") {
		t.Fatalf("an expired manifest must force a fresh run:\n%s", second)
	}
}
