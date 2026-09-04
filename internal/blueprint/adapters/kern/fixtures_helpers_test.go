package kern

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// FixtureResult holds a materialized fixture repo ready for testing.
type FixtureResult struct {
	RepoPath    string   // absolute path to the git repo
	StagedFiles []string // files that are staged (git add'd) and ready to check
}

// boundariesJSON is the shared boundary rule used by every fixture: only the
// web -> db edge is forbidden; all other edges are allowed by default.
const boundariesJSON = `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`

// runGit runs git in dir.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// writeGoFile writes a .go file with a package import comment so kern can index it.
func writeGoFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// writeBoundaries writes .kern/boundaries.json.
func writeBoundaries(t *testing.T, dir, content string) {
	t.Helper()
	bp := filepath.Join(dir, ".kern", "boundaries.json")
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		t.Fatalf("mkdir .kern: %v", err)
	}
	if err := os.WriteFile(bp, []byte(content), 0o644); err != nil {
		t.Fatalf("write boundaries: %v", err)
	}
}

// writeGoMod writes a minimal go.mod so the fixture repo is a valid Go module.
func writeGoMod(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// initRepo creates a git repo in dir with an initial commit.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@test.test")
	runGit(t, dir, "config", "user.name", "test")
}

// commitAll stages everything and commits.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", msg)
}

// stage stages specific files.
func stage(t *testing.T, dir string, files ...string) {
	t.Helper()
	args := append([]string{"add"}, files...)
	runGit(t, dir, args...)
}

// ArchitectureClean materializes a clean-architecture fixture with no violations.
func ArchitectureClean(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	commitAll(t, dir, "init")
	// Stage a harmless change for the check.
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\nfunc Extra() {}\n")
	stage(t, dir, "web/web.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/web.go"}}
}

// LegalDependency materializes a repo with legal cross-boundary dependencies:
// web -> api and api -> db are allowed; only web -> db is forbidden.
func LegalDependency(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "api/api.go", "package api\n\nimport \"example.com/repo/db\"\n\nfunc Handle() { db.Query() }\n")
	writeGoFile(t, dir, "web/web.go", "package web\n\nimport \"example.com/repo/api\"\n\nfunc Render() { api.Handle() }\n")
	commitAll(t, dir, "init")
	// Stage a harmless change that keeps the dependency chain legal.
	writeGoFile(t, dir, "web/web.go", "package web\n\nimport \"example.com/repo/api\"\n\nfunc Render() { api.Handle() }\nfunc Render2() { api.Handle() }\n")
	stage(t, dir, "web/web.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/web.go"}}
}

// IllegalDependency materializes a repo where a staged change introduces a new
// illegal web -> db dependency.
func IllegalDependency(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	commitAll(t, dir, "init")
	// Staged change: new file with a forbidden db import.
	writeGoFile(t, dir, "web/web2.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc Extra() { db.Query() }\n")
	stage(t, dir, "web/web2.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/web2.go"}}
}

// MultipleViolations materializes a repo where a staged change introduces two
// new illegal web -> db dependencies.
func MultipleViolations(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	commitAll(t, dir, "init")
	// Staged change: two new files, each with a forbidden db import.
	writeGoFile(t, dir, "web/a.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc A() { db.Query() }\n")
	writeGoFile(t, dir, "web/b.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc B() { db.Query() }\n")
	stage(t, dir, "web/a.go", "web/b.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/a.go", "web/b.go"}}
}

// PreexistingViolationUnrelatedChange materializes a repo where the base commit
// already violates web -> db but the staged change (a clean new file) does not
// introduce any new violation. This is the key fixture for the new-change
// principle.
func PreexistingViolationUnrelatedChange(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	// BASE commit ALREADY has the violation.
	writeGoFile(t, dir, "web/web.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc Handle() { db.Query() }\n")
	commitAll(t, dir, "init with pre-existing violation")
	// Staged change: add a CLEAN unrelated file.
	writeGoFile(t, dir, "web/clean.go", "package web\n\nfunc Helper() {}\n")
	stage(t, dir, "web/clean.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/clean.go"}}
}

// RenameScenario materializes a repo where the staged change is a pure file
// rename with unchanged content, introducing no new violation.
func RenameScenario(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/handler.go", "package web\n\nfunc Handle() {}\n")
	commitAll(t, dir, "init")
	// Staged change: rename handler.go -> handlers.go, content unchanged.
	runGit(t, dir, "mv", "web/handler.go", "web/handlers.go")
	stage(t, dir, "web/handlers.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/handlers.go"}}
}

// NewFileScenario materializes a repo where the staged change is a new file
// with no violations.
func NewFileScenario(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	commitAll(t, dir, "init")
	// Staged change: new clean web file that does not import db.
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	stage(t, dir, "web/web.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/web.go"}}
}

// DeletedImportScenario materializes a repo where the staged change REMOVES an
// illegal import, fixing a pre-existing violation.
func DeletedImportScenario(t *testing.T) FixtureResult {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeGoMod(t, dir)
	writeBoundaries(t, dir, boundariesJSON)
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	// BASE commit has the violation.
	writeGoFile(t, dir, "web/web.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc Handle() { db.Query() }\n")
	commitAll(t, dir, "init with violation")
	// Staged change: remove the db import (now clean).
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	stage(t, dir, "web/web.go")
	return FixtureResult{RepoPath: dir, StagedFiles: []string{"web/web.go"}}
}
