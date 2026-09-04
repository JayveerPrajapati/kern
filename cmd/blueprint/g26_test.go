package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestG26_SandboxTestsOptIn wires the sandbox build/test check (spec Phase 8)
// behind the opt-in --tests flag (G26).
//
// The sandbox check is registered in NO default entry point: `blueprint check
// --staged` (the pre-commit hook) must stay fast and must NOT run it, while
// `blueprint check --tests` runs `go build ./...`/`go test ./...` in an
// isolated git worktree and BLOCKs on failure per the `tests` policy.
//
// Setup: a git repo whose committed HEAD deliberately fails to compile (the
// sandbox worktree is built at HEAD), plus one clean staged change so the
// validation pipeline has a change set to run on.
func TestG26_SandboxTestsOptIn(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g4WriteFile(t, dir, "main.go", "package main\nfunc main() { println(\"ok\") }\n")
	// Compile-breaking file committed to HEAD: the sandbox builds a worktree
	// at HEAD, so it must be in HEAD for `go build ./...` to fail there.
	g4WriteFile(t, dir, "broken.go", "package main\n\nfunc broken() { this is invalid }\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init with broken file")
	// Stage a clean change so validation runs (empty change => NOOP PASS).
	g4WriteFile(t, dir, "extra.go", "package main\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "extra.go")

	// 1. `blueprint check --tests` runs the sandbox check: BLOCK with a
	// sandbox:build-failure finding routed to the `tests` category (which
	// defaults to block — the Step 3 category fix).
	out, code := g26Check(t, bin, dir, "--tests")
	res := g26Parse(t, out)
	if code != 1 {
		t.Fatalf("check --tests exit = %d, want 1 (BLOCK); output:\n%s", code, out)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("check --tests status = %q, want %q; output:\n%s", res.Status, domain.StatusBlock, out)
	}
	bf := g26Finding(res, "sandbox:build-failure")
	if bf == nil {
		t.Fatalf("check --tests missing sandbox:build-failure finding; output:\n%s", out)
	}
	if bf.Category != domain.CategoryTests {
		t.Errorf("sandbox:build-failure category = %q, want %q (must route to the block-default tests policy)", bf.Category, domain.CategoryTests)
	}

	// 2. `blueprint check --staged` (no --tests) must NOT run the sandbox
	// check: no sandbox finding, and the result is not BLOCK from sandbox.
	out, code = g26Check(t, bin, dir)
	res = g26Parse(t, out)
	if bf := g26Finding(res, "sandbox:build-failure"); bf != nil {
		t.Fatalf("check without --tests ran the sandbox check (unexpected finding %q); output:\n%s", bf.RuleID, out)
	}
	if res.Status == domain.StatusBlock {
		t.Fatalf("check without --tests status = %q, want not %q; output:\n%s", res.Status, domain.StatusBlock, out)
	}
	if code != 0 {
		t.Fatalf("check without --tests exit = %d, want 0 (PASS/WARN, not BLOCK); output:\n%s", code, out)
	}
}

// g26Check runs `blueprint check --repo=dir --format=json [extra...]` and
// returns stdout and the exit code (stdout stays clean JSON; stderr is only
// surfaced on launch failure).
func g26Check(t *testing.T, binPath, dir string, extra ...string) (string, int) {
	t.Helper()
	args := []string{"check", "--repo", dir, "--format=json"}
	args = append(args, extra...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint check: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
	}
	return stdout.String(), code
}

// g26Parse decodes the JSON ValidationResult emitted by `check --format=json`.
func g26Parse(t *testing.T, out string) domain.ValidationResult {
	t.Helper()
	var res domain.ValidationResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse check output: %v\n%s", err, out)
	}
	return res
}

// g26Finding returns the finding with the given ruleID, or nil.
func g26Finding(res domain.ValidationResult, ruleID string) *domain.Finding {
	for i := range res.Findings {
		if res.Findings[i].RuleID == ruleID {
			return &res.Findings[i]
		}
	}
	return nil
}
