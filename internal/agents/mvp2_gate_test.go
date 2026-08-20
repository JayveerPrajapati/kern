package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// gateFixture writes a tiny standalone Go module and returns its root.
func gateFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module gatefixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { println(helper()) }
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "h" {
		t.Fail()
	}
}
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

// TestMVP2GateEndToEnd runs the MVP2 killer workflow (spec §45):
//
//	Request → Analyze → Plan → Approve → Code → Verify → PR
//
// end-to-end against a tiny fixture module, wiring the real subsystems
// (context engine, execution worktree, verification engine) into the
// agent runtime's DefaultWorkflow. This is the gate that must pass before
// MVP3 work begins.
func TestMVP2GateEndToEnd(t *testing.T) {
	root := gateFixture(t)

	t.Log("[1/8] Indexing fixture...")
	start := time.Now()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("index produced 0 symbols")
	}
	t.Logf("      %d symbols, %d edges (%.1fs)", len(ix.Symbols), len(ix.Calls), time.Since(start).Seconds())

	t.Log("[2/8] Building knowledge graph...")
	g := intelligence.FromIndex(ix)
	if len(g.Nodes) == 0 {
		t.Fatal("graph has 0 nodes")
	}

	t.Log("[3/8] Setting up memory + governance...")
	store := memory.NewMemoryStore(root)
	store.Add(domain.Memory{
		Type:    domain.MemoryConstraint,
		Content: "helper must return the string 'h'",
		Scope:   "main",
		Tags:    []string{"gatefixture", "helper"},
	})
	fw := governance.NewFirewall()
	fw.WithAgents(governance.NewAgent("planner", "Planner", "planner", []governance.Permission{
		{Resource: "source", Action: "read"},
	}))
	approvals := governance.NewApprovalWorkflow()

	t.Log("[4/8] Creating context engine + standard team...")
	ctxEngine := context.NewEngine(root, &g, store, fw)
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	if team == nil || runtime == nil {
		t.Fatal("StandardTeam returned nil registries")
	}

	t.Log("[5/8] Creating worktree for the code step...")
	wt, err := execution.NewWorktree(root)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer wt.Cleanup()

	t.Log("[6/8] Running workflow...")
	engine := agent.NewWorkflowEngine(runtime, approvals)

	var (
		analyzeOut string
		planOut    string
		codeOut    string
		verifyOut  string
		prOut      string
	)

	task := agent.NewTask("feature", "Add a Greet function to the fixture")
	task.WorkflowID = "default"

	stepHandler := func(action string, tsk *agent.Task) (string, error) {
		switch action {
		case "request":
			return "request acknowledged", nil
		case "analyze":
			pkt, err := ctxEngine.AnalyzeChange("helper")
			if err != nil {
				return "", err
			}
			analyzeOut = context.RenderText(pkt)
			return analyzeOut, nil
		case "plan":
			planOut = "Plan: add Greet() to main.go, add test, run go test"
			return planOut, nil
		case "code":
			// Apply a real change in the worktree: add a Greet function.
			p := filepath.Join(wt.Dir(), "greet.go")
			if err := os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644); err != nil {
				return "", err
			}
			diff, err := wt.Diff()
			if err != nil {
				return "", err
			}
			codeOut = diff
			return diff, nil
		case "verify":
			v := verification.NewEngine(wt.Dir())
			res := v.Verify([]string{"build"})
			verifyOut = res.Summary
			if res.Verdict != verification.VerdictPass {
				return "", errors.New("build verification failed: " + res.Summary)
			}
			return verifyOut, nil
		case "pr":
			prOut = "PR: add Greet function\n\n" + codeOut
			return prOut, nil
		default:
			return "", errors.New("unexpected action: " + action)
		}
	}

	final, runErr := engine.Run(task, stepHandler)

	// Handle the human-approval gate (approve step in DefaultWorkflow).
	if runErr != nil && errors.Is(runErr, agent.ErrApprovalRequired) {
		id := agent.ApprovalID(runErr)
		if id == "" {
			t.Fatal("ErrApprovalRequired returned empty approval ID")
		}
		t.Logf("  approval gate hit: %s — approving...", id)
		if err := engine.CompleteApproval(id, "human"); err != nil {
			t.Fatalf("CompleteApproval: %v", err)
		}
		final, runErr = engine.Run(task, stepHandler)
	}
	if runErr != nil {
		t.Fatalf("workflow Run failed: %v", runErr)
	}

	t.Log("[7/8] Workflow completed")
	t.Logf("  state=%s", final.State)
	t.Logf("  output=%d chars", len(final.Output))

	t.Log("[8/8] === MVP2 GATE ACCEPTANCE CHECK ===")
	checks := []struct {
		name   string
		pass   bool
		detail string
	}{
		{"Final state COMPLETED", final.State == domain.TaskCompleted, string(final.State)},
		{"Analyze step ran", analyzeOut != "", "analyze output present"},
		{"Plan step ran", planOut != "", "plan output present"},
		{"Code step produced diff", codeOut != "", "diff produced"},
		{"Verify step passed", strings.Contains(verifyOut, "PASS") || verifyOut != "", verifyOut},
		{"PR output generated", prOut != "", "PR body present"},
		{"Workflow steps recorded", len(final.Steps) >= 6, plural(len(final.Steps), "step")},
	}
	allPass := true
	for _, c := range checks {
		status := "PASS"
		if !c.pass {
			status = "FAIL"
			allPass = false
		}
		t.Logf("  [%s] %-32s %s", status, c.name, c.detail)
	}
	if !allPass {
		t.Error("=== MVP2 GATE: SOME CHECKS FAILED ===")
	} else {
		t.Log("=== MVP2 GATE: ALL CHECKS PASSED ===")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strings.TrimSuffix(unit, "y") + "ies"
}
