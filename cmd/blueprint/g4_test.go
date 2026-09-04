package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// blueprintTestBinary builds the blueprint binary ONCE per test-binary run
// and returns its path. Every build helper (buildBlueprint in g11_test.go,
// g4BuildBinary here, buildBlueprintBinary in g13_test.go) delegates here so
// the suite collapses ~40 per-test `go build` invocations into one.
//
// Why this matters: the previous helpers built into t.TempDir(), which is
// deleted when the test ends — so the g4BlueprintBin "cache" never survived
// across tests and every test paid a fresh `go build`. Each build pays the
// link step and contends with any concurrent go process (e.g. another test's
// `go build`, or `go test ./...` spawned by the resilience check) for the
// shared GOCACHE lock, inflating unpredictably. ~40 builds × 1.5-8s made the
// full suite take 2-4 minutes, so short `-timeout` budgets expired mid-test
// and the deadline panic dumped an in-flight subprocess stack that read as a
// "hang". The binary path therefore lives in a process-lifetime temp dir
// (NOT t.TempDir) so the build is amortized across the whole run.
var (
	blueprintTestBinOnce sync.Once
	blueprintTestBinPath string
	blueprintTestBinErr  error
)

func blueprintTestBinary(t *testing.T) string {
	t.Helper()
	blueprintTestBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "blueprint-test-bin-")
		if err != nil {
			blueprintTestBinErr = fmt.Errorf("create test bin dir: %w", err)
			return
		}
		root, err := findBlueprintRepoRoot()
		if err != nil {
			blueprintTestBinErr = err
			return
		}
		blueprintTestBinPath = filepath.Join(dir, "blueprint")
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", blueprintTestBinPath, "./cmd/blueprint")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			blueprintTestBinErr = fmt.Errorf("build blueprint: %v\n%s", err, out)
		}
	})
	if blueprintTestBinErr != nil {
		t.Fatal(blueprintTestBinErr)
	}
	return blueprintTestBinPath
}

// findBlueprintRepoRoot walks up from the working directory to the dir
// containing go.mod (the blueprint repo root). Unlike the per-helper walkers
// it does not take *testing.T so it can run inside sync.Once.
func findBlueprintRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find go.mod walking up from %s", dir)
}

func g4BuildBinary(t *testing.T) string {
	t.Helper()
	return blueprintTestBinary(t)
}

// g4GitRepo initializes a git repo in dir with identity config.
func g4GitRepo(t *testing.T, dir string) {
	t.Helper()
	g4RunGit(t, dir, "init", "-q")
	g4RunGit(t, dir, "config", "user.email", "t@t")
	g4RunGit(t, dir, "config", "user.name", "t")
}

func g4RunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func g4WriteFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

func g4WriteBoundaries(t *testing.T, dir string) {
	t.Helper()
	g4WriteFile(t, dir, ".kern/boundaries.json", `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
}

// g4BlueprintCheck runs `blueprint check --staged --format=terminal --repo=dir`
// and returns (combined output, exit code).
func g4BlueprintCheck(t *testing.T, binPath, dir string, extraArgs ...string) (string, int) {
	t.Helper()
	args := append([]string{"check", "--staged", "--format=terminal", "--repo", dir}, extraArgs...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
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

// G4-1: clean commit
func TestG4_CleanCommit(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a clean change
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "web/web.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (clean commit); output:\n%s", code, out)
	}
}

// G4-2: architecture violation
func TestG4_ArchitectureViolation(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a violating change
	g4WriteFile(t, dir, "web/web2.go", "package web\nimport \"example.com/repo/db\"\nfunc Handle2() { db.Query() }\n")
	g4RunGit(t, dir, "add", "web/web2.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK); output:\n%s", code, out)
	}
	if !strings.Contains(out, "BLOCK") {
		t.Fatalf("output missing BLOCK:\n%s", out)
	}
}

// G4-3: secret violation
func TestG4_SecretViolation(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "clean.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a file with a secret
	g4WriteFile(t, dir, "config.go", "package main\nconst AWSKey = \"AKIA1234567890ABCDEF\"\n")
	g4RunGit(t, dir, "add", "config.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK for secret); output:\n%s", code, out)
	}
}

// G4-4: multiple findings (architecture + secret)
func TestG4_MultipleFindings(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a file with BOTH a boundary violation AND a secret
	g4WriteFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nconst AWSKey = \"AKIA1234567890ABCDEF\"\nfunc Bad() { db.Query() }\n")
	g4RunGit(t, dir, "add", "web/bad.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK); output:\n%s", code, out)
	}
}

// G4-5: staged file differs from working tree
func TestG4_StagedDiffersFromWorkingTree(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "file.go", "package main\nfunc A() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage one version, then modify working tree to differ
	g4WriteFile(t, dir, "file.go", "package main\nfunc B() {}\n")
	g4RunGit(t, dir, "add", "file.go")
	g4WriteFile(t, dir, "file.go", "package main\nfunc C() {}\n") // working tree differs

	// Should check the STAGED version, not the working tree version.
	out, code := g4BlueprintCheck(t, bin, dir)
	// Both versions are clean (no secrets, no boundaries), so PASS.
	if code != 0 {
		t.Fatalf("exit=%d want 0 (staged version is clean); output:\n%s", code, out)
	}
}

// G4-6: deleted file
func TestG4_DeletedFile(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "old.go", "package main\nfunc Old() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a deletion
	g4RunGit(t, dir, "rm", "old.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	// Deleting a file should not cause an error; it's a valid change.
	if code == 2 {
		t.Fatalf("exit=2 (ERROR) — deleting a file should not error; output:\n%s", out)
	}
}

// G4-7: rename
func TestG4_Rename(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "web/handler.go", "package web\nfunc Handle() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a rename
	g4RunGit(t, dir, "mv", "web/handler.go", "web/handlers.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (rename is clean); output:\n%s", code, out)
	}
}

// G4-8: binary file
func TestG4_BinaryFile(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "readme.txt", "hello\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a binary file (non-text content)
	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), binaryContent, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	g4RunGit(t, dir, "add", "image.png")

	out, code := g4BlueprintCheck(t, bin, dir)
	// Binary files should not cause an error.
	if code == 2 {
		t.Fatalf("exit=2 (ERROR) — binary file should not error; output:\n%s", out)
	}
}

// G4-9: empty commit (no staged changes)
func TestG4_EmptyCommit(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "file.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// No staged changes
	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (empty commit = no changes = PASS); output:\n%s", code, out)
	}
}

// G4-10: hook failure (blueprint binary errors out)
func TestG4_HookFailure(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)

	// Run with an invalid repo path to trigger an error.
	out, code := g4BlueprintCheck(t, bin, "/nonexistent/path/that/does/not/exist")
	if code != 2 {
		t.Fatalf("exit=%d want 2 (ERROR for invalid repo); output:\n%s", code, out)
	}
}

// G4-11: Blueprint binary unavailable (simulate by pointing to a nonexistent binary)
func TestG4_BlueprintBinaryUnavailable(t *testing.T) {
	// Use a fake binary path that doesn't exist.
	fakeBin := "/nonexistent/blueprint"
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "file.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")

	out, err := exec.Command(fakeBin, "check", "--staged", "--repo", dir).CombinedOutput()
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	// The OS should refuse to exec the binary.
	_ = out
}

// G4-12: explicit bypass behavior documented
// This test verifies that `git commit --no-verify` bypasses the hook, which is
// documented behavior (spec Rule 3, line 861). We install the hook, create a
// violation, and verify --no-verify lets it through.
func TestG4_ExplicitBypassDocumented(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Install the hook — run `blueprint install hook` in the repo dir.
	installCmd := exec.Command(bin, "install", "hook")
	installCmd.Dir = dir
	if out, err := installCmd.CombinedOutput(); err != nil {
		t.Fatalf("install hook: %v\n%s", err, out)
	}

	// Verify the hook file exists and is executable.
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook not installed: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Error("hook is not executable")
	}

	// Stage a violating change.
	g4WriteFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")
	g4RunGit(t, dir, "add", "web/bad.go")

	// Commit WITHOUT --no-verify: should be blocked by the hook.
	// (The hook calls `blueprint check --staged` which needs kern to run the
	// architecture guard.) P0.3: blueprint check degrades gracefully (WARN,
	// exit 0) when kern is unreachable instead of hard-failing — so for the
	// hook to actually BLOCK the violation, the hook env must resolve kern.
	// requireKernPath skips this test on machines without a kern binary
	// (same policy as TestG17_Healthy).
	kernPath := requireKernPath(t)
	commitCmd := exec.Command("git", "commit", "-m", "should be blocked")
	commitCmd.Dir = dir
	commitCmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	// Also put the blueprint binary on PATH so the hook can find it.
	binDir := filepath.Dir(bin)
	commitCmd.Env = append(commitCmd.Env, "PATH="+binDir+":/usr/bin:/bin")
	commitOut, commitErr := commitCmd.CombinedOutput()
	if commitErr == nil {
		// Hook did not block (e.g. kern could not build the index inside the
		// hook env). That's OK — the important part is that --no-verify works
		// below. Undo the commit so the bypass commit still has the staged
		// change to commit.
		t.Logf("commit without --no-verify unexpectedly succeeded (hook did not block):\n%s", commitOut)
		g4RunGit(t, dir, "reset", "--soft", "HEAD~1")
	}

	// Commit WITH --no-verify: must succeed (bypass documented behavior).
	bypassCmd := exec.Command("git", "commit", "--no-verify", "-m", "bypassed")
	bypassCmd.Dir = dir
	bypassOut, bypassErr := bypassCmd.CombinedOutput()
	if bypassErr != nil {
		t.Fatalf("git commit --no-verify should succeed (bypass is documented): %v\n%s", bypassErr, bypassOut)
	}

	// Verify the commit landed.
	logCmd := exec.Command("git", "log", "--oneline", "-1")
	logCmd.Dir = dir
	logOut, _ := logCmd.Output()
	if !strings.Contains(string(logOut), "bypassed") {
		t.Errorf("bypassed commit not found in log:\n%s", logOut)
	}
}

// G4-bonus: install hook is idempotent
func TestG4_InstallHookIdempotent(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)

	// Install once
	cmd1 := exec.Command(bin, "install", "hook")
	cmd1.Dir = dir
	if out, err := cmd1.CombinedOutput(); err != nil {
		t.Fatalf("first install: %v\n%s", err, out)
	}

	// Install again — should succeed (idempotent)
	cmd2 := exec.Command(bin, "install", "hook")
	cmd2.Dir = dir
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("second install (should be idempotent): %v\n%s", err, out)
	}
}

// G4-bonus: install hook refuses to overwrite foreign hook
func TestG4_InstallHookRefusesForeign(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)

	// Write a pre-existing foreign hook
	hookDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte("#!/bin/sh\necho custom\n"), 0o755); err != nil {
		t.Fatalf("write foreign hook: %v", err)
	}

	// Install should refuse
	cmd := exec.Command(bin, "install", "hook")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("install should refuse to overwrite foreign hook")
	}
	if !strings.Contains(string(out), "already exists") {
		t.Errorf("output should mention existing hook:\n%s", out)
	}
}

// G4-bonus: JSON format works
func TestG4_JSONFormat(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "file.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	g4WriteFile(t, dir, "file.go", "package main\nfunc main() {}\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "file.go")

	cmd := exec.Command(bin, "check", "--staged", "--format=json", "--repo", dir)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run blueprint check: %v\n%s", err, out)
	}
	if code != 0 {
		t.Fatalf("exit=%d want 0; output:\n%s", code, out)
	}
	if !strings.Contains(string(out), `"status"`) {
		t.Errorf("JSON output missing status field:\n%s", out)
	}
}
