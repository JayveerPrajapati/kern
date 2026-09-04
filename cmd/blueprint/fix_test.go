package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- G22 test helpers ---

// g22FixRepo builds a tiny git repo with a committed architecture violation:
// web/web.go imports db, which .kern/boundaries.json forbids. The fix command
// is expected to validate the proposed (fixed) content against this setup.
func g22FixRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nimport \"example.com/repo/db\"\nfunc Handle() { db.Query() }\n")
	g4WriteFile(t, dir, ".gitignore", ".blueprint/\n.kern/index.json\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	return dir
}

// g22RunFix runs `blueprint fix --repo dir <args>` with KERN_BINARY set and
// returns (combined output, exit code).
func g22RunFix(t *testing.T, binPath, dir, kernPath string, args ...string) (string, int) {
	t.Helper()
	argv := append([]string{"fix", "--repo", dir}, args...)
	cmd := exec.Command(binPath, argv...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint fix: %v\n%s", err, out)
		}
	}
	return string(out), code
}

// g22GitStatus returns `git status --porcelain` for dir ("" when clean).
func g22GitStatus(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status --porcelain: %v", err)
	}
	return string(out)
}

// g22WorktreePaths lists the worktree paths registered in the fixture repo
// (`git worktree list --porcelain`). A fix run registers its sandbox worktree
// in the fixture repo's git metadata and removes the registration on cleanup,
// so a leftover registration means cleanup did not run. Checking the repo's
// own git metadata — instead of scanning os.TempDir() for the
// blueprint-sandbox-* prefix — is race-free: other test packages running
// concurrently under `go test ./...` also create and remove same-prefixed
// worktree dirs in the shared temp dir.
func g22WorktreePaths(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	m := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			m[strings.TrimSpace(strings.TrimPrefix(line, "worktree "))] = true
		}
	}
	return m
}

// --- G22 tests ---

// TestG22_CleanFix: a proposed fix that removes the forbidden import verifies
// clean (exit 0, no findings) and leaves the USER TREE byte-identical: the
// repo file content and git state are unchanged after the command runs.
func TestG22_CleanFix(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)

	webPath := filepath.Join(dir, "web", "web.go")
	before, err := os.ReadFile(webPath)
	if err != nil {
		t.Fatalf("read fixture web/web.go: %v", err)
	}
	gitBefore := g22GitStatus(t, dir)

	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "web/web.go",
		"--content", "package web\nfunc Handle() {}\n")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (PASS); output:\n%s", code, out)
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("output missing PASS verdict:\n%s", out)
	}
	if strings.Contains(out, "Findings:") && strings.Contains(out, "[BLOCK]") {
		t.Fatalf("unexpected BLOCK findings in clean fix:\n%s", out)
	}
	// The exact diff the fix would produce must be rendered.
	if !strings.Contains(out, "Diff:") || !strings.Contains(out, "web/web.go") {
		t.Fatalf("output missing the diff section:\n%s", out)
	}

	// The user's tree is untouched: file bytes and git state are identical.
	after, err := os.ReadFile(webPath)
	if err != nil {
		t.Fatalf("read fixture web/web.go after fix: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("user repo file was modified:\nbefore: %s\nafter:  %s", before, after)
	}
	if gitAfter := g22GitStatus(t, dir); gitAfter != gitBefore {
		t.Fatalf("git state changed:\nbefore: %q\nafter:  %q", gitBefore, gitAfter)
	}
}

// TestG22_FixBlockedBySecret: proposed content containing a live token is
// BLOCKED (exit 1) and the output never echoes the snippet (redaction
// invariant — the diff section is omitted when findings remain).
func TestG22_FixBlockedBySecret(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)
	// Detected by gitleaks AND the in-house kern scanner, so the gate passes
	// whether the incumbent binary is installed.
	const snippet = "AKIA1234567890ABCDEF"
	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "web/web.go",
		"--content", "package web\nconst password = \""+snippet+"\"\nfunc Handle() {}\n")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (fix blocked); output:\n%s", code, out)
	}
	if !strings.Contains(out, "BLOCK") {
		t.Fatalf("output missing BLOCK finding:\n%s", out)
	}
	if strings.Contains(out, snippet) {
		t.Fatalf("redaction invariant violated: snippet %q leaked into output:\n%s", snippet, out)
	}
	// The diff is replaced by a redaction note: neither the proposed content
	// nor a hunk that could carry it may be echoed.
	if !strings.Contains(out, "Diff: omitted") {
		t.Fatalf("output missing the diff redaction note:\n%s", out)
	}
	if strings.Contains(out, "const password") {
		t.Fatalf("proposed content leaked into output despite findings:\n%s", out)
	}
}

// TestG22_Confinement: a --file path that escapes the repo is rejected with a
// tool error (exit 2) before any worktree is created or mutated.
func TestG22_Confinement(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)

	before := g22WorktreePaths(t, dir)
	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "../escape.go", "--content", "package escape\n")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (tool error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "invalid path") {
		t.Fatalf("output missing confinement error:\n%s", out)
	}
	// No worktree may have been created (and registered in this repo) for an
	// escaping path.
	after := g22WorktreePaths(t, dir)
	for w := range after {
		if !before[w] {
			t.Fatalf("confinement rejection registered a worktree: %s", w)
		}
	}
}

// TestG22_NotARepo: --repo pointing at a directory without .git is a tool
// error (exit 2).
func TestG22_NotARepo(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := t.TempDir() // no git init

	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "a.go", "--content", "package a\n")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (tool error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "not a git repository") {
		t.Fatalf("output missing 'not a git repository':\n%s", out)
	}
}

// TestG22_WorktreeCleanedUp: after a run, no sandbox worktree remains
// registered in the repo (the sandbox cleanup works).
func TestG22_WorktreeCleanedUp(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)

	before := g22WorktreePaths(t, dir)
	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "web/web.go",
		"--content", "package web\nfunc Handle() {}\n")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}

	after := g22WorktreePaths(t, dir)
	for w := range after {
		if !before[w] {
			t.Fatalf("leftover sandbox worktree after fix: %s", w)
		}
	}
}

// TestG22_JsonShape: --json output parses and contains the checks, findings,
// and diffs keys.
func TestG22_JsonShape(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)

	out, code := g22RunFix(t, binPath, dir, kernPath, "--json",
		"--file", "web/web.go",
		"--content", "package web\nfunc Handle() {}\n")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("parse --json output: %v\n%s", err, out)
	}
	for _, key := range []string{"status", "exit_code", "checks", "findings", "diffs"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("json output missing key %q:\n%s", key, out)
		}
	}
	var checks []json.RawMessage
	if err := json.Unmarshal(raw["checks"], &checks); err != nil {
		t.Fatalf("parse checks: %v", err)
	}
	if len(checks) < 3 {
		t.Fatalf("checks = %d, want >= 3 (architecture/secrets/duplication)", len(checks))
	}
	var diffs []json.RawMessage
	if err := json.Unmarshal(raw["diffs"], &diffs); err != nil {
		t.Fatalf("parse diffs: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs = %d, want 1 for a single proposed file", len(diffs))
	}
}

// TestG22_NoFiles: an invocation without any --file/--content pair is a usage
// error (exit 2).
func TestG22_NoFiles(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)
	dir := g22FixRepo(t)

	out, code := g22RunFix(t, binPath, dir, kernPath)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "--file") {
		t.Fatalf("output missing usage hint mentioning --file:\n%s", out)
	}
}

// TestG22_WarnOnlyExitsOneWithNote: a fix run whose only finding is the
// architecture:not-enforced WARN (repo with no .kern/) still exits 1 — ANY
// remaining finding (WARN or BLOCK) forces the repair loop to iterate (see
// fixExitCode) — and prints the clarifying note explaining that contract. The
// note is also carried into --json output as the additive "note" field.
func TestG22_WarnOnlyExitsOneWithNote(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := g4BuildBinary(t)

	// Repo WITHOUT .kern/ so the architecture guard fires
	// architecture:not-enforced (WARN) for the non-empty proposed change.
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4WriteFile(t, dir, ".gitignore", ".blueprint/\n.kern/index.json\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	const wantNote = "note: fix exits 1 while ANY finding remains (WARN or BLOCK); iterate the repair loop until the fix verifies clean (exit 0)"

	// Text mode: a NEW file with clean content → WARN-only result, exit 1 (the
	// repair loop must iterate) and the note line in the output.
	out, code := g22RunFix(t, binPath, dir, kernPath,
		"--file", "extra.go",
		"--content", "package extra\nfunc X() {}\n")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (WARN finding still blocks the loop); output:\n%s", code, out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("output missing WARN verdict:\n%s", out)
	}
	if !strings.Contains(out, wantNote) {
		t.Fatalf("output missing the warn-exit note %q:\n%s", wantNote, out)
	}

	// JSON mode: additive "note" field with the same text, status WARN, exit 1.
	out, code = g22RunFix(t, binPath, dir, kernPath, "--json",
		"--file", "extra.go",
		"--content", "package extra\nfunc X() {}\n")
	if code != 1 {
		t.Fatalf("json exit code = %d, want 1; output:\n%s", code, out)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("parse json output: %v\n%s", err, out)
	}
	if got := string(raw["status"]); got != `"WARN"` {
		t.Fatalf("json status = %s, want \"WARN\"; output:\n%s", got, out)
	}
	if got := string(raw["exit_code"]); got != "1" {
		t.Fatalf("json exit_code = %s, want 1; output:\n%s", got, out)
	}
	var note string
	if err := json.Unmarshal(raw["note"], &note); err != nil {
		t.Fatalf("json missing parseable \"note\" field: %v; output:\n%s", err, out)
	}
	if note != wantNote {
		t.Fatalf("json note = %q, want %q", note, wantNote)
	}
}
