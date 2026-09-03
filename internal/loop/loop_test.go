package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/coder"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Autonomy
		ok   bool
	}{
		{"L0", L0, true}, {"l4", L4, true}, {"5", L5, true}, {"L3", L3, true},
		{"", 0, false}, {"L6", 0, false}, {"-1", 0, false}, {"Lx", 0, false},
	} {
		got, err := ParseLevel(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Fatalf("ParseLevel(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
			}
		} else if err == nil {
			t.Fatalf("ParseLevel(%q) expected error, got %v", tc.in, got)
		}
	}
}

func TestAutonomyGating(t *testing.T) {
	// L0: analysis only. Code/deploy/learn/protect skipped.
	if L0.AllowsStage(stageCode) || L0.AllowsStage(stageDeploy) || L0.AllowsStage(stageLearn) || L0.AllowsStage(stageProtect) {
		t.Fatal("L0 must not allow act/write stages")
	}
	if !L0.AllowsStage(stageObserve) || !L0.AllowsStage(stageVerify) || !L0.AllowsStage(stageIntent) || !L0.AllowsStage(stageRemember) {
		t.Fatal("L0 must allow read-only stages")
	}
	// L2 => code, not deploy.
	if !L2.AllowsStage(stageCode) || L2.AllowsStage(stageDeploy) || L2.AllowsStage(stageProtect) {
		t.Fatal("L2 must allow code but not deploy/protect")
	}
	// L4 => deploy allowed; L5 => everything.
	if !L4.AllowsStage(stageDeploy) || !L4.AllowsStage(stageProtect) {
		t.Fatal("L4 must allow deploy and protect")
	}
	if !L5.AllowsStage(stageCode) || !L5.AllowsStage(stageDeploy) {
		t.Fatal("L5 must allow all act stages")
	}
}

func loopFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module loopfixture\n\ngo 1.20\n",
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

// TestPhase15LoopEndToEnd runs the closed loop (Intent → Plan → Code →
// Verify → Deploy → Observe → Learn) against a tiny fixture with a real
// worktree, verification, approval gate and memory store, and asserts the
// outcome is learned back into memory.
// TestLoopLearningSurfacesConstraint proves the continuous-learning extractor is
// wired into the production loop: after the learn stage records a lesson over a
// few similar memories, a recurring pattern is surfaced and a MemoryConstraint
// is written back so future context retrieval surfaces it.
func TestLoopLearningSurfacesConstraint(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	// Seed two similar lessons in the same scope so the pattern is recurring.
	for i := 0; i < 2; i++ {
		if _, err := store.Add(domain.Memory{
			Type:    domain.MemoryLesson,
			Content: fmt.Sprintf("checkout failure %d", i),
			Source:  "loop",
			Scope:   "service:checkout",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	lp, err := NewLoop(LoopConfig{
		Root:             t.TempDir(),
		Level:            L4,
		Scope:            "project",
		Mem:              store,
		Learning:         learning.New(store),
		PatternThreshold: 2,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	// Running the wired learn path writes a lesson and triggers extraction.
	if _, err := lp.learn("checkout health", &Result{ObservedHealthy: true}); err != nil {
		t.Fatalf("learn: %v", err)
	}

	// A constraint memory must have been written for the recurring pattern.
	constraints, err := store.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("List constraints: %v", err)
	}
	if len(constraints) == 0 {
		t.Fatal("expected a MemoryConstraint to be written by continuous learning")
	}
	found := false
	for _, c := range constraints {
		if c.Scope == "scope:service:checkout" && c.Source == "learning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a learning constraint for scope:service:checkout, got %+v", constraints)
	}
}

// TestLoopLearningSkippedWhenUnwired: without a learning extractor the loop
// still records a lesson and never touches the pattern extractor (nil-safe).
func TestLoopLearningSkippedWhenUnwired(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	lp, err := NewLoop(LoopConfig{Root: t.TempDir(), Level: L4, Mem: store})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if _, err := lp.learn("checkout health", &Result{ObservedHealthy: true}); err != nil {
		t.Fatalf("learn: %v", err)
	}
	ms, _ := store.List(domain.MemoryConstraint)
	if len(ms) != 0 {
		t.Fatalf("expected no constraints when learning is unwired, got %d", len(ms))
	}
}

// TestLearnWritesEpisodicAndLesson verifies the learn stage writes BOTH an
// episodic memory (raw event context: intent, level, deployed, observed_healthy)
// and the existing lesson (derived takeaway).
func TestLearnWritesEpisodicAndLesson(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	lp, err := NewLoop(LoopConfig{Root: t.TempDir(), Level: L4, Mem: store})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if _, err := lp.learn("greet health", &Result{ObservedHealthy: true, Deployed: true}); err != nil {
		t.Fatalf("learn: %v", err)
	}

	ms, _ := store.List("")
	var haveLesson, haveEpisodic bool
	for _, m := range ms {
		switch m.Type {
		case domain.MemoryLesson:
			haveLesson = true
		case domain.MemoryEpisodic:
			haveEpisodic = true
			if !strings.Contains(m.Content, "intent=") || !strings.Contains(m.Content, "deployed=") {
				t.Fatalf("episodic memory content missing raw event fields: %q", m.Content)
			}
		}
	}
	if !haveLesson {
		t.Fatal("expected at least one MemoryLesson to be written by learn")
	}
	if !haveEpisodic {
		t.Fatal("expected at least one MemoryEpisodic to be written by learn")
	}
}

func TestPhase15LoopEndToEnd(t *testing.T) {
	root := loopFixture(t)

	// Production mutation is disabled by default (KERN_ALLOW_DEPLOY).
	// This test exercises the deploy stage, so it must opt in explicitly.
	t.Setenv("KERN_ALLOW_DEPLOY", "1")

	// Production source with no errors in the observe window → healthy.
	src := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	src.Ingest(runtime.Event{ID: "e1", Type: runtime.EventLog, Service: "checkout", Severity: "info", Message: "normal", Timestamp: now})

	mem := memory.NewMemoryStore(t.TempDir())
	incSt := incident.NewStore(t.TempDir())

	lp, err := NewLoop(LoopConfig{
		Root:      root,
		Level:     L4,
		Service:   "checkout",
		Since:     now.Add(-time.Minute),
		Source:    src,
		Mem:       mem,
		Incidents: incSt,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a Greet function", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		switch stage {
		case stagePlan:
			return "plan: add Greet() + test", nil
		case stageCode:
			p := filepath.Join(wt.Dir(), "greet.go")
			if err := os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644); err != nil {
				return "", err
			}
			return "wrote greet.go", nil
		}
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The deploy stage is handled by the deployer (NoopDeployer here since
	// KERN_DEPLOY_COMMAND is unset), which reports a simulated success.
	if !res.Deployed {
		t.Fatal("deploy stage did not run")
	}
	if res.Diff == "" {
		t.Fatal("expected a code diff")
	}
	if !res.ObservedHealthy {
		t.Fatal("observe should report healthy (no errors in window)")
	}
	if res.Learned == nil {
		t.Fatal("expected a learned memory")
	}

	// The learned memory must be persisted and reflect the outcome.
	ms, _ := mem.List("")
	if len(ms) == 0 {
		t.Fatal("learned memory not persisted")
	}
	if ms[0].Type != domain.MemoryLesson {
		t.Fatalf("learned memory type = %q, want lesson", ms[0].Type)
	}

	// BOTH a lesson (derived takeaway) and an episodic memory (raw event)
	// must be written by the learn stage.
	var haveLesson, haveEpisodic bool
	for _, m := range ms {
		switch m.Type {
		case domain.MemoryLesson:
			haveLesson = true
		case domain.MemoryEpisodic:
			haveEpisodic = true
			if !strings.Contains(m.Content, "intent=") || !strings.Contains(m.Content, "deployed=") {
				t.Fatalf("episodic memory content missing raw event fields: %q", m.Content)
			}
		}
	}
	if !haveLesson {
		t.Fatal("expected a MemoryLesson to be written by the learn stage")
	}
	if !haveEpisodic {
		t.Fatal("expected a MemoryEpisodic to be written by the learn stage")
	}

	// All 9 stages present in order.
	if len(res.Stages) != 9 {
		t.Fatalf("stages = %d, want 9", len(res.Stages))
	}
	// Autonomy gating: L0 skips act stages.
	l0, _ := NewLoop(LoopConfig{Root: root, Level: L0, Service: "checkout", Source: src})
	res0, err := l0.Run(root, nil)
	if err != nil {
		t.Fatalf("L0 run: %v", err)
	}
	for _, st := range res0.Stages {
		if (st.Stage == stageCode || st.Stage == stageDeploy || st.Stage == stageProtect || st.Stage == stageLearn) && st.Status != "skipped:below-autonomy" {
			t.Fatalf("L0 stage %q status = %q, want skipped", st.Stage, st.Status)
		}
	}
}

// TestL5ProofGate verifies the loop enforces the L5 proof gate at runtime: at
// L5 autonomy with a nil (or incomplete) proof map, write/act stages (code,
// deploy, protect) are skipped and the run fails closed; once all required
// proofs are satisfied they are permitted. This closes the prior gap where
// the L5 proof machinery (AllowsStageWithProofs/L5Proofs) was dead code.
func TestL5ProofGate(t *testing.T) {
	root := loopFixture(t)

	// 1) L5 with nil proofs: write stages must be skipped (fail closed).
	lp, err := NewLoop(LoopConfig{Root: root, Level: L5, Mem: memory.NewMemoryStore(t.TempDir()), Service: "checkout", Source: runtime.NewStore()})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("make a low-risk change", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, st := range res.Stages {
		switch st.Stage {
		case stageCode, stageDeploy, stageProtect:
			if st.Status != "skipped:below-autonomy" {
				t.Fatalf("L5 with nil proofs: stage %q status = %q, want skipped:below-autonomy", st.Stage, st.Status)
			}
		}
	}

	// 2) L5 with ALL proofs satisfied: write stages must be allowed.
	proofs := L5Proofs{}
	for _, req := range RequiredL5Proofs() {
		proofs[req] = true
	}
	lpFull, err := NewLoop(LoopConfig{Root: root, Level: L5, Proofs: proofs, Mem: memory.NewMemoryStore(t.TempDir()), Service: "checkout", Source: runtime.NewStore()})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	resFull, err := lpFull.Run("make a low-risk change", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, st := range resFull.Stages {
		switch st.Stage {
		case stageCode, stageDeploy, stageProtect:
			if st.Status == "skipped:below-autonomy" {
				t.Fatalf("L5 with full proofs: stage %q must not be skipped", st.Stage)
			}
		}
	}

	// 3) L5 with a PARTIAL proof map: still fails closed (any missing proof).
	partial := L5Proofs{ProofPolicy: true, ProofVerification: true}
	lpPart, err := NewLoop(LoopConfig{Root: root, Level: L5, Proofs: partial, Mem: memory.NewMemoryStore(t.TempDir()), Service: "checkout", Source: runtime.NewStore()})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	resPart, err := lpPart.Run("make a low-risk change", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, st := range resPart.Stages {
		switch st.Stage {
		case stageCode, stageDeploy, stageProtect:
			if st.Status != "skipped:below-autonomy" {
				t.Fatalf("L5 with partial proofs: stage %q status = %q, want skipped", st.Stage, st.Status)
			}
		}
	}
}

// TestRememberStage verifies the REMEMBER stage recalls engineering memory
// relevant to the intent before planning, surfacing it on res.Remembered.
func TestRememberStage(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	if _, err := store.Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: "always use a Greet function for the greeting service",
		Source:  "loop",
		Scope:   "project",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L0, Service: "checkout", Source: runtime.NewStore(), Mem: store})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a Greet function", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Remembered) == 0 {
		t.Fatal("expected remembered memories, got none")
	}
	if res.Remembered[0].Content != "always use a Greet function for the greeting service" {
		t.Fatalf("remembered content = %q", res.Remembered[0].Content)
	}
	for _, st := range res.Stages {
		if st.Stage == stageRemember && st.Status != "ok" {
			t.Fatalf("remember stage status = %q, want ok", st.Status)
		}
	}
}

// TestProtectStage verifies the loop invokes governance approval (PROTECT)
// between verify and deploy at L4, and that it fails CLOSED when the approval
// is not explicitly approved: a freshly-created "pending" approval must block
// the deploy (Bug #2), not let it proceed.
func TestProtectStage(t *testing.T) {
	appr := governance.NewApprovalWorkflow()
	src := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	src.Ingest(runtime.Event{ID: "e1", Type: runtime.EventLog, Service: "checkout", Severity: "info", Message: "ok", Timestamp: now})

	var deployed bool
	lp, err := NewLoop(LoopConfig{
		Root:    loopFixture(t),
		Level:   L4,
		Service: "checkout",
		Since:   now.Add(-time.Minute),
		Source:  src,
		Mem:     memory.NewMemoryStore(t.TempDir()),
		Appr:    appr,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		if stage == stageDeploy {
			deployed = true
		}
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected Run to fail closed on a pending (not approved) approval")
	}
	if deployed {
		t.Fatal("deploy proceeded despite approval not being approved; must fail closed")
	}
	if res.Protected {
		t.Fatal("expected res.Protected = false when the approval was not approved")
	}
	if len(appr.Pending()) == 0 {
		t.Fatal("expected a pending approval created by the protect stage")
	}
	for _, st := range res.Stages {
		if st.Stage == stageProtect && st.Status != "error" {
			t.Fatalf("protect stage status = %q, want error (fail closed)", st.Status)
		}
	}
}

// fakeProvider is a scripted agent.Provider used to exercise the coder wiring
// without a real LLM. It returns a fixed response per Generate call.
type fakeProvider struct {
	responses []string
	idx       int
}

// Generate implements agent.Provider. It returns the next canned response.
func (f *fakeProvider) Generate(prompt string, options ...agent.Option) (string, error) {
	if f.idx >= len(f.responses) {
		return "", fmt.Errorf("fakeProvider: out of responses")
	}
	r := f.responses[f.idx]
	f.idx++
	return r, nil
}

// coderCommentPatch is a unified diff (git apply / patch -p1 friendly) that
// adds a comment to the fixture's main.go. It keeps the module buildable so
// the coder's verify round passes.
const coderCommentPatch = "```diff\n" +
	"--- a/main.go\n" +
	"+++ b/main.go\n" +
	"@@ -1,3 +1,4 @@\n" +
	" package main\n" +
	" \n" +
	"+// coder applied\n" +
	" func helper() string { return \"h\" }\n" +
	"```\n"

// TestLoopUsesCoderWhenStepNil verifies that when no StepFunc is supplied but
// LoopConfig.Coder is set, the loop delegates the code stage to the coder agent
// at an autonomy level that permits the code stage (L2). The code stage must
// run, surface a coder-produced output, and yield a non-empty diff.
func TestLoopUsesCoderWhenStepNil(t *testing.T) {
	prov := &fakeProvider{responses: []string{coderCommentPatch}}
	a := coder.New(prov)

	lp, err := NewLoop(LoopConfig{
		Root:  loopFixture(t),
		Level: L2,
		Mem:   memory.NewMemoryStore(t.TempDir()),
		Coder: a,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a comment", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var codeStage *StageResult
	for i := range res.Stages {
		if res.Stages[i].Stage == stageCode {
			codeStage = &res.Stages[i]
			break
		}
	}
	if codeStage == nil {
		t.Fatal("code stage did not run")
	}
	if codeStage.Status != "ok" {
		t.Fatalf("code stage status = %q, want ok; output=%q", codeStage.Status, codeStage.Output)
	}
	if !strings.Contains(codeStage.Output, "coder") {
		t.Fatalf("code stage output should mention the coder, got %q", codeStage.Output)
	}
	if res.Diff == "" {
		t.Fatal("expected a non-empty diff after the coder ran")
	}
	if !strings.Contains(res.Diff, "coder applied") {
		t.Fatalf("diff should contain the applied patch, got %q", res.Diff)
	}
}

// TestLoopCoderNoProvider verifies the loop surfaces a clear error when the
// wired coder has no LLM provider: the code stage must report it and the loop
// must return an error.
func TestLoopCoderNoProvider(t *testing.T) {
	a := coder.New(nil) // no provider → Code returns ErrNoProvider

	lp, err := NewLoop(LoopConfig{
		Root:  loopFixture(t),
		Level: L2,
		Mem:   memory.NewMemoryStore(t.TempDir()),
		Coder: a,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a comment", nil)
	if err == nil {
		t.Fatal("expected an error when the coder has no provider")
	}

	found := false
	for _, st := range res.Stages {
		if st.Stage == stageCode {
			found = true
			if !strings.Contains(st.Output, "no LLM provider") {
				t.Fatalf("code stage output should mention no LLM provider, got %q", st.Output)
			}
			if st.Status != "error" {
				t.Fatalf("code stage status = %q, want error", st.Status)
			}
		}
	}
	if !found {
		t.Fatal("code stage did not run")
	}
}

// TestSafetyBudgetPause verifies that when the wired SafetyBudget is exceeded
// the loop PAUSES (fail-closed): it stops executing subsequent stages, sets
// Result.BudgetPaused, and returns instead of proceeding through the rest of
// the pipeline.
func TestSafetyBudgetPause(t *testing.T) {
	budget := &domain.SafetyBudget{MaxToolCalls: 1}

	lp, err := NewLoop(LoopConfig{
		Root:   loopFixture(t),
		Level:  L2,
		Budget: budget,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.BudgetPaused {
		t.Fatal("expected the loop to PAUSE when the safety budget is exceeded")
	}
	// With MaxToolCalls=1 the loop must stop after the first stage — it must
	// never continue to the subsequent stages of the pipeline.
	if len(res.Stages) >= 9 {
		t.Fatalf("expected the loop to stop early after the budget was exceeded, got %d stages", len(res.Stages))
	}
}

func TestLoopRunContextCancellation(t *testing.T) {
	lp, err := NewLoop(LoopConfig{
		Root:  loopFixture(t),
		Level: L2,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	res, err := lp.RunContext(ctx, "cancelled intent", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "", nil
	})
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if res == nil || len(res.Stages) == 0 || res.Stages[0].Status != "cancelled" {
		t.Fatalf("expected stage to be cancelled, got: %+v", res)
	}
}

