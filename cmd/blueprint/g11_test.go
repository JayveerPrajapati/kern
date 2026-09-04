package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- G11 test helpers ---

// g11Repo creates a git repo with a base branch (main) and a feature branch
// (feature) with the given extra files. Returns the repo dir.
func g11Repo(t *testing.T, mainFiles, featureFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	g11Git(t, dir, "init", "-q", "-b", "main")
	g11Git(t, dir, "config", "user.email", "t@t")
	g11Git(t, dir, "config", "user.name", "t")
	g11Write(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	// .blueprint/metrics.json (local metrics) and .kern/index.json (the kern
	// index cache) are generated local data: CI runs create them and they must
	// not show up as untracked changes in the working tree. Config files such
	// as .blueprint/config.yaml and .kern/boundaries.json stay tracked — they
	// are the declared repository configuration.
	g11Write(t, dir, ".gitignore", ".blueprint/metrics.json\n.blueprint/audit/\n.blueprint/verdict-cache/\n.blueprint/receipts/\n.kern/index.json\n")
	g11Write(t, dir, ".kern/boundaries.json", `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	for path, content := range mainFiles {
		g11Write(t, dir, path, content)
	}
	g11Git(t, dir, "add", "-A")
	g11Git(t, dir, "commit", "-qm", "base")
	// Create feature branch.
	g11Git(t, dir, "checkout", "-b", "feature")
	for path, content := range featureFiles {
		g11Write(t, dir, path, content)
	}
	g11Git(t, dir, "add", "-A")
	// Allow empty commits so an empty PR (no changes) can still create a
	// feature branch ref for CI to diff against.
	g11Git(t, dir, "commit", "--allow-empty", "-qm", "feature")
	g11Git(t, dir, "checkout", "main")
	return dir
}

func g11Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func g11Write(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// buildBlueprint returns the suite-wide blueprint binary (built once per run;
// see blueprintTestBinary in g4_test.go).
func buildBlueprint(t *testing.T) string {
	t.Helper()
	return blueprintTestBinary(t)
}

// runCICommand runs `blueprint ci` against a repo and returns stdout, stderr, exit code, and artifact.
func runCICommand(t *testing.T, binPath, repoDir, kernPath string, extraArgs ...string) (string, string, int, CIArtifact) {
	t.Helper()
	artifactPath := filepath.Join(t.TempDir(), "result.json")
	args := []string{"ci", "--repo", repoDir, "--base", "main", "--head", "feature", "--artifact-file", artifactPath, "--no-human"}
	args = append(args, extraArgs...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint ci: %v", err)
		}
	}
	// Read artifact.
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact CIArtifact
	if err := json.Unmarshal(artifactBytes, &artifact); err != nil {
		t.Fatalf("parse artifact: %v\nraw: %s", err, artifactBytes)
	}
	return stdout.String(), stderr.String(), exitCode, artifact
}

// --- G11 Tests ---

// G11-1: clean PR — no violations, should PASS.
func TestG11_CleanPR(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/clean.go": "package web\nfunc Clean() {}\n",
		},
	)

	stdout, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)

	_ = stdout
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0 (PASS); artifact: %+v", exitCode, artifact)
	}
	if artifact.Status != "PASS" {
		t.Errorf("status = %s, want PASS", artifact.Status)
	}
	if artifact.FilesChanged != 1 {
		t.Errorf("files_changed = %d, want 1", artifact.FilesChanged)
	}
	if artifact.FindingsCount != 0 {
		t.Errorf("findings_count = %d, want 0", artifact.FindingsCount)
	}
}

// G11-2: policy violation PR — should BLOCK.
func TestG11_PolicyViolationPR(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	_, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)

	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1 (BLOCK)", exitCode)
	}
	if artifact.Status != "BLOCK" {
		t.Errorf("status = %s, want BLOCK", artifact.Status)
	}
	if artifact.FindingsCount == 0 {
		t.Error("expected findings for violation PR")
	}
	if len(artifact.Findings) == 0 {
		t.Error("expected finding details in artifact")
	}
}

// G11-3: stale config — missing .blueprint/config.yaml should still work
// (defaults), but a malformed config should ERROR.
func TestG11_StaleConfig(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)

	// Repo with a malformed config.
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":               "package db\nfunc Query() {}\n",
			"web/web.go":             "package web\nfunc Handle() {}\n",
			".blueprint/config.yaml": "{ this is not valid yaml: [",
		},
		map[string]string{
			"web/extra.go": "package web\nfunc Extra() {}\n",
		},
	)

	_, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)

	if exitCode != 3 {
		t.Errorf("exit code = %d, want 3 (config error)", exitCode)
	}
	if artifact.Status != "ERROR" {
		t.Errorf("status = %s, want ERROR", artifact.Status)
	}
}

// G11-4: missing Blueprint binary — when kern binary is unavailable.
func TestG11_MissingBinary(t *testing.T) {
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/extra.go": "package web\nfunc Extra() {}\n",
		},
	)

	// Run with a nonexistent kern binary.
	artifactPath := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command(binPath, "ci", "--repo", dir, "--base", "main", "--head", "feature", "--artifact-file", artifactPath, "--no-human")
	cmd.Env = append(os.Environ(), "KERN_BINARY=/nonexistent/kern/binary")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	if exitCode != 2 {
		t.Errorf("exit code = %d, want 2 (ERROR for missing binary)", exitCode)
	}

	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact CIArtifact
	json.Unmarshal(artifactBytes, &artifact)
	if artifact.Status != "ERROR" {
		t.Errorf("status = %s, want ERROR", artifact.Status)
	}
	if artifact.Error == "" {
		t.Error("expected error message for missing binary")
	}
}

// G11-5: deterministic result across clean runners — running CI twice on the
// same repo should produce identical artifacts (modulo timestamps and duration).
func TestG11_DeterministicAcrossRuns(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	_, _, _, artifact1 := runCICommand(t, binPath, dir, kernPath)
	_, _, _, artifact2 := runCICommand(t, binPath, dir, kernPath)

	// Compare deterministic fields (ignore timestamps and duration which vary).
	if artifact1.Status != artifact2.Status {
		t.Errorf("status differs: %s vs %s", artifact1.Status, artifact2.Status)
	}
	if artifact1.ExitCode != artifact2.ExitCode {
		t.Errorf("exit_code differs: %d vs %d", artifact1.ExitCode, artifact2.ExitCode)
	}
	if artifact1.FilesChanged != artifact2.FilesChanged {
		t.Errorf("files_changed differs: %d vs %d", artifact1.FilesChanged, artifact2.FilesChanged)
	}
	if artifact1.FindingsCount != artifact2.FindingsCount {
		t.Errorf("findings_count differs: %d vs %d", artifact1.FindingsCount, artifact2.FindingsCount)
	}
	if len(artifact1.Findings) != len(artifact2.Findings) {
		t.Fatalf("findings length differs: %d vs %d", len(artifact1.Findings), len(artifact2.Findings))
	}
	for i := range artifact1.Findings {
		f1, f2 := artifact1.Findings[i], artifact2.Findings[i]
		if f1.RuleID != f2.RuleID {
			t.Errorf("finding[%d].rule_id differs: %s vs %s", i, f1.RuleID, f2.RuleID)
		}
		if f1.File != f2.File {
			t.Errorf("finding[%d].file differs: %s vs %s", i, f1.File, f2.File)
		}
		if f1.Line != f2.Line {
			t.Errorf("finding[%d].line differs: %d vs %d", i, f1.Line, f2.Line)
		}
		if f1.Message != f2.Message {
			t.Errorf("finding[%d].message differs: %s vs %s", i, f1.Message, f2.Message)
		}
	}
}

// G11-6: JSON artifact output — the --json flag should emit JSON to stdout.
func TestG11_JSONArtifactOutput(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	stdout, _, _, _ := runCICommand(t, binPath, dir, kernPath, "--json")

	// stdout should contain valid JSON.
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		t.Fatal("expected JSON on stdout with --json flag")
	}
	var artifact CIArtifact
	if err := json.Unmarshal([]byte(stdout), &artifact); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nraw: %s", err, stdout)
	}
	if artifact.Status != "BLOCK" {
		t.Errorf("artifact status = %s, want BLOCK", artifact.Status)
	}
}

// G11-7: human-readable summary — without --no-human, stderr should contain
// a readable summary.
func TestG11_HumanReadableSummary(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	artifactPath := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command(binPath, "ci", "--repo", dir, "--base", "main", "--head", "feature", "--artifact-file", artifactPath)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Run()

	summary := stderr.String()
	// Must contain key sections.
	for _, expected := range []string{"Blueprint CI", "Status:", "Base:", "Head:", "Findings:"} {
		if !strings.Contains(summary, expected) {
			t.Errorf("summary missing %q:\n%s", expected, summary)
		}
	}
	// Must mention the finding.
	if !strings.Contains(summary, "boundary-violation") && !strings.Contains(summary, "forbidden") {
		t.Errorf("summary should mention the violation:\n%s", summary)
	}
}

// G11-bonus: no local daemon state — CI should work even with no .kern index
// present (it builds its own).
func TestG11_NoLocalDaemonState(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	// Remove any pre-existing .kern index (simulating a fresh CI checkout).
	os.RemoveAll(filepath.Join(dir, ".kern", "index.json"))

	_, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)

	// Should still detect the violation — CI builds its own index.
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1 (BLOCK); CI should work without local index", exitCode)
	}
	if artifact.Status != "BLOCK" {
		t.Errorf("status = %s, want BLOCK", artifact.Status)
	}
}

// G11-bonus: empty PR (no changes) should PASS.
func TestG11_EmptyPR(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{}, // no feature changes
	)

	_, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0 (PASS for empty PR)", exitCode)
	}
	if artifact.FilesChanged != 0 {
		t.Errorf("files_changed = %d, want 0", artifact.FilesChanged)
	}
}

// G11-8: `blueprint ci -head <sha>` must validate against a throwaway detached
// worktree and never mutate the user's working tree: no checkout of the real
// repo, no leftover worktrees, same branch, clean status.
func TestG11_HeadRefWorktreeNoMutation(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	// Snapshot the source repo state before the run.
	branchBefore, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse --abbrev-ref HEAD: %v", err)
	}
	statusBefore, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status --porcelain: %v", err)
	}

	// Resolve the two commit SHAs: main is the first commit, feature the second.
	headSha, err := exec.Command("git", "-C", dir, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatalf("rev-parse feature: %v", err)
	}
	baseSha, err := exec.Command("git", "-C", dir, "rev-parse", "main").Output()
	if err != nil {
		t.Fatalf("rev-parse main: %v", err)
	}

	// Override the default --head/--base with the resolved SHAs (extraArgs are
	// appended after the defaults, so they win).
	_, stderr, exitCode, artifact := runCICommand(t, binPath, dir, kernPath,
		"--head", strings.TrimSpace(string(headSha)),
		"--base", strings.TrimSpace(string(baseSha)),
	)

	// The head ref adds web/bad.go which violates the web->db boundary: BLOCK.
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1 (BLOCK); artifact: %+v\nstderr: %s", exitCode, artifact, stderr)
	}
	if artifact.Status != "BLOCK" {
		t.Errorf("status = %s, want BLOCK", artifact.Status)
	}

	// The source repo must be untouched: same branch, clean tree, one worktree.
	branchAfter, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse after: %v", err)
	}
	if got := strings.TrimSpace(string(branchAfter)); got != strings.TrimSpace(string(branchBefore)) {
		t.Errorf("branch changed: before=%q after=%q", strings.TrimSpace(string(branchBefore)), got)
	}
	statusAfter, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("status --porcelain after: %v", err)
	}
	if got := string(statusAfter); got != string(statusBefore) {
		t.Errorf("working tree changed:\nbefore:\n%s\nafter:\n%s", statusBefore, got)
	}
	wtOut, err := exec.Command("git", "-C", dir, "worktree", "list").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if lines := len(strings.Split(strings.TrimSpace(string(wtOut)), "\n")); lines != 1 {
		t.Errorf("worktree list shows %d entries, want exactly 1 (the main checkout):\n%s", lines, wtOut)
	}
}
