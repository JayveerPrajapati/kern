package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
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

// TestReadStdinNonTTY (report A15 regression) pins the v0.9.5.2 fix: reading
// stdin must never block on a character device (interactive terminal), return
// real piped content for a regular file, and respect the size cap.
func TestReadStdinNonTTY(t *testing.T) {
	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })

	// A pipe (non-TTY) delivers content.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("chore: fix pipe input\n")
	w.Close()
	os.Stdin = r
	b, err := readStdin()
	if err != nil {
		t.Fatalf("readStdin(pipe): %v", err)
	}
	if string(b) != "chore: fix pipe input\n" {
		t.Errorf("readStdin(pipe) = %q", b)
	}
	r.Close()

	// An empty regular file reads as empty (non-TTY, no hang).
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = f
	if b, err := readStdin(); err != nil || len(b) != 0 {
		t.Fatalf("readStdin(empty file) = %q, %v; want empty, nil", b, err)
	}
	f.Close()

	// A character device (/dev/null on POSIX) must return immediately with
	// nil content — this is the interactive-terminal case that used to hang
	// until v0.9.5.2.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = devNull
	if b, err := readStdin(); err != nil || b != nil {
		t.Fatalf("readStdin(char device) = %q, %v; want nil, nil (no blocking)", b, err)
	}
	devNull.Close()
}

func TestRunCompactAbsolutePathInCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(cwd, "main.go")
	runCompact([]string{abs})
}

func TestRenderStatelessPlanNetNewFeature(t *testing.T) {
	pkt := domain.ContextPacket{
		Symbols: []domain.Symbol{{Name: "RandomTest"}},
		Files:   []domain.File{{Path: "foo_test.go"}},
	}
	rendered := renderStatelessPlan("Add REST endpoint for consumer lag", pkt)
	if strings.Contains(rendered, "RandomTest") || strings.Contains(rendered, "foo_test.go") {
		t.Errorf("rendered plan should not contain random test components for net-new feature, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Scope: net-new feature") {
		t.Errorf("expected net-new feature scope, got:\n%s", rendered)
	}
}

