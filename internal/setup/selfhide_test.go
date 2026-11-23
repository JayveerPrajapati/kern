package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/gitblocks"
)

// selfhide_test.go — pins kern's self-hiding promise (F-A6): a fresh
// `kern setup` in a brand-new repository must leave `git status --porcelain`
// EMPTY. Every file project wiring writes must be covered by the
// kern-generated .gitignore block (internal/gitblocks.gitignoreBody), so a
// newcomer never has to classify untracked entries by hand. Anything this
// test flags as uncovered is a file setup generates but the block misses.

// selfHideHome isolates every home-relative resolver so the wiring (and any
// incidental agent detection via home paths) stays inside a temp dir.
func selfHideHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
}

// TestSetupFreshRepoLeavesGitStatusClean wires a full agent set into a
// freshly `git init`-ed repository and asserts the generated .gitignore
// block hides every written path.
func TestSetupFreshRepoLeavesGitStatusClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	selfHideHome(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}

	// The full agent list (detect=false) wires every repo-scoped target —
	// a superset of what a detected-only `kern setup` writes.
	agents := []string{
		"opencode", "claude", "cursor", "antigravity", "continue",
		"windsurf", "kiro", "vscode", "copilot", "gemini", "codex",
	}
	for _, s := range Wire(root, agents, false, false) {
		if s.Path == "" && s.Note != "" && !s.Installed {
			t.Fatalf("wire step %s failed: %s", s.Agent, s.Note)
		}
	}
	// Instruction files are written for DETECTED agents only; force the
	// same writers so CLAUDE.md / GEMINI.md / .agents/rules/kern.md /
	// .github/copilot-instructions.md / .cursor/rules/kern.mdc land too.
	wireInstructions(root, []string{"claude", "gemini", "antigravity", "copilot", "cursor"}, "all", false)

	out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Fatalf("a fresh setup must leave git status clean; the .gitignore block does not cover:\n%s", got)
	}
}

// TestSetupFreshRepoDetectModeClean repeats the check for the DEFAULT
// `kern setup` invocation shape (detection on, no explicit agent list): the
// universal files (AGENTS.md, .mcp.json, opencode.json, .opencode/,
// .agents/skills/, .gitignore) land even when no agent is detected.
func TestSetupFreshRepoDetectModeClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	selfHideHome(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	for _, s := range Wire(root, nil, true, false) {
		if s.Path == "" && s.Note != "" && !s.Installed {
			t.Fatalf("wire step %s failed: %s", s.Agent, s.Note)
		}
	}
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Fatalf("a detected-mode fresh setup must leave git status clean; uncovered:\n%s", got)
	}
}

// TestGeneratedBlockMatchesRepoCuratedSection keeps the generator's entry
// list and the kern repo's own curated "kern generated" .gitignore section
// equivalent: running setup in this very repo must not dirty .gitignore.
func TestGeneratedBlockMatchesRepoCuratedSection(t *testing.T) {
	repoGitignore := filepath.Join("..", "..", ".gitignore")
	b, err := os.ReadFile(repoGitignore)
	if err != nil {
		t.Skipf("repo .gitignore not reachable: %v", err)
	}
	const marker = "# --- kern generated (agent wiring, machine-specific) ---"
	const closeMarker = "# --- end kern generated ---"
	start := strings.Index(string(b), marker)
	end := strings.Index(string(b), closeMarker)
	if start < 0 || end < start {
		t.Fatalf("repo .gitignore has no complete kern generated section")
	}
	curated := strings.TrimSpace(string(b)[start : end+len(closeMarker)])

	// The generated block for a repo whose .gitignore holds nothing else
	// must be byte-identical to that curated section.
	fresh := t.TempDir()
	res := gitblocks.GitignoreGenerated(fresh)
	if !res.Installed {
		t.Fatalf("gitignore generation failed: %s", res.Note)
	}
	got, err := os.ReadFile(filepath.Join(fresh, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != curated {
		t.Errorf("generator block and repo curated section diverged.\n--- generator ---\n%s\n--- repo curated ---\n%s", got, curated)
	}
}
