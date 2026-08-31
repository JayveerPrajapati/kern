package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loopCliFixture writes a tiny single-package Go module so the loop's
// deterministic verify stage (a go build in the sandbox worktree) is fast and
// passes. This exercises runLoopCLI without re-indexing the repo.
func loopCliFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module cliloopfixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { _ = helper() }
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// TestRunLoopCLI asserts the loop CLI helper runs offline at L0 (read-only:
// act/write stages skipped) and renders the stage timeline plus the outcome.
func TestRunLoopCLI(t *testing.T) {
	root := loopCliFixture(t)
	out, err := runLoopCLI(root, "", "add a helper")
	if err != nil {
		t.Fatalf("runLoopCLI: %v", err)
	}
	if !strings.Contains(out, "level: L0") {
		t.Fatalf("expected default L0 level; got:\n%s", out)
	}
	for _, stage := range []string{"intent", "code", "verify", "deploy", "observe", "learn"} {
		if !strings.Contains(out, stage+":") {
			t.Fatalf("missing stage timeline entry for %q; got:\n%s", stage, out)
		}
	}
	// L0 gates: code/deploy/learn are skipped below the autonomy level.
	if !strings.Contains(out, "code: skipped:below-autonomy") {
		t.Fatalf("code stage not autonomy-gated; got:\n%s", out)
	}
	if !strings.Contains(out, "deployed: false") {
		t.Fatalf("deployed should be false at L0; got:\n%s", out)
	}
	if !strings.Contains(out, "observed-healthy:") {
		t.Fatalf("missing observed-healthy outcome; got:\n%s", out)
	}
}

// TestRunLoopCLIInvalidLevel asserts an out-of-range level fails closed.
func TestRunLoopCLIInvalidLevel(t *testing.T) {
	root := loopCliFixture(t)
	if _, err := runLoopCLI(root, "L9", "x"); err == nil {
		t.Fatal("expected error for invalid level L9")
	}
}

// TestRenderTeamText asserts the team roster renders all 7 specialists and the
// current task count.
func TestRenderTeamText(t *testing.T) {
	root := t.TempDir()
	text, err := renderTeamText(root)
	if err != nil {
		t.Fatalf("renderTeamText: %v", err)
	}
	if !strings.Contains(text, "specialists:") {
		t.Fatalf("missing specialists header; got:\n%s", text)
	}
	for _, role := range []string{"planner", "architect", "coder", "reviewer", "security", "tester", "sre"} {
		if !strings.Contains(text, "(role "+role+")") {
			t.Fatalf("missing specialist role %q; got:\n%s", role, text)
		}
	}
	if !strings.Contains(text, "tasks: 0") {
		t.Fatalf("expected empty task list on a fresh team; got:\n%s", text)
	}
}

// TestRunWorkflowCLI asserts the agent-team workflow CLI runs offline: it
// selects the team, drives the pre-gate steps, and parks at the human approval
// gate with a resolvable approval ID — the exit gate over the CLI.
func TestRunWorkflowCLI(t *testing.T) {
	root := loopCliFixture(t)
	text, err := runWorkflowCLI(root, "Greet")
	if err != nil {
		t.Fatalf("runWorkflowCLI: %v", err)
	}
	if !strings.Contains(text, "WAITING_FOR_APPROVAL") {
		t.Fatalf("run parked state missing; got:\n%s", text)
	}
	if !strings.Contains(text, "approval required:") {
		t.Fatalf("approval gate not surfaced; got:\n%s", text)
	}
	for _, want := range []string{"analyze", "plan"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pre-gate steps missing %q; got:\n%s", want, text)
		}
	}
}
