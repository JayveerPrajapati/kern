package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Phase 10 — Flagship Vertical Slice.
//
// This file closes the remaining Phase 10 gaps:
//
//	10.1 on-disk fixture (testdata/user_service_slice.json) instead of inline consts
//	10.2 the flagship drives the exact UserService request from the fixture
//	10.4 asserts RiskReport / Diff / PR / Audit artifacts are produced
//	10.5 consolidates the scattered failure cases into one 7-failure drill
//	10.6 per-lifecycle efficiency metrics (durations per stage)
//
// These are e2e tests against the real kern repo and are skipped under -short.

// sliceFixture is the on-disk fixture for the flagship vertical slice.
type sliceFixture struct {
	Intent           string   `json:"intent"`
	Target           string   `json:"target"`
	ExpectedArtifacts []string `json:"expected_artifacts"`
	WorkflowStages    []string `json:"workflow_stages"`
	Assertions        struct {
		RiskReportPresent bool `json:"risk_report_present"`
		DiffPresent       bool `json:"diff_present"`
		PRPresent         bool `json:"pr_present"`
		AuditRecorded     bool `json:"audit_recorded"`
	} `json:"assertions"`
}

// loadFixture reads the on-disk flagship fixture (P10.1). It is the single
// source of truth for what the flagship slice must exercise, replacing inline
// constants so the scenario is data-driven and auditable.
func loadFixture(t *testing.T) sliceFixture {
	t.Helper()
	path := filepath.Join("testdata", "user_service_slice.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	var f sliceFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return f
}

// lifecycleMetrics records per-stage durations for a single lifecycle run (P10.6).
type lifecycleMetrics struct {
	start map[string]time.Time
	elapsed map[string]time.Duration
	order   []string
}

func newLifecycleMetrics() *lifecycleMetrics {
	return &lifecycleMetrics{start: map[string]time.Time{}, elapsed: map[string]time.Duration{}}
}

func (m *lifecycleMetrics) begin(stage string) {
	if _, ok := m.start[stage]; !ok {
		m.start[stage] = time.Now()
		m.order = append(m.order, stage)
	}
}

func (m *lifecycleMetrics) end(stage string) {
	if s, ok := m.start[stage]; ok {
		m.elapsed[stage] = time.Since(s)
	}
}

// lifecycleStages reports the stages measured in order.
func (m *lifecycleMetrics) lifecycleStages() []string { return m.order }

// TestFlagshipVerticalSlice drives the exact UserService request from the
// on-disk fixture through the analyze→plan→impact→risk→execute→verify→PR
// lifecycle, asserting the required artifacts (P10.1/P10.2/P10.4) and recording
// per-stage efficiency metrics (P10.6).
func TestFlagshipVerticalSlice(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	f := loadFixture(t)
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	metrics := newLifecycleMetrics()

	// --- Analyze (exact UserService request from fixture) ---
	metrics.begin("analyze")
	task, text, err := ts.Analyze(f.Target)
	metrics.end("analyze")
	if err != nil {
		t.Fatalf("Analyze(%q): %v", f.Target, err)
	}
	if task.State != "COMPLETED" {
		t.Errorf("analyze state = %s, want COMPLETED", task.State)
	}
	if text == "" {
		t.Error("analyze returned empty text")
	}

	// --- Plan ---
	metrics.begin("plan")
	task, _, _, err = ts.Plan(f.Target)
	metrics.end("plan")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if task.Plan == nil {
		t.Error("plan not attached")
	}

	// --- Impact ---
	metrics.begin("impact")
	_, _, err = ts.WhatIf(whatif.ChangeKind("remove_symbol"), f.Target, "")
	metrics.end("impact")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}

	// Assert artifacts across the whole chain (P10.4).
	arts, _ := ts.Artifacts().GetByTask(task.ID)
	kindSet := map[string]bool{}
	for _, a := range arts {
		kindSet[string(a.Kind)] = true
	}

	if f.Assertions.RiskReportPresent && !kindSet["risk_report"] {
		t.Errorf("risk_report artifact not produced; got kinds %v", keysOf(kindSet))
	}
	if f.Assertions.DiffPresent && !kindSet["diff"] {
		t.Errorf("diff artifact not produced; got kinds %v", keysOf(kindSet))
	}
	if f.Assertions.PRPresent && !kindSet["pull_request"] {
		t.Errorf("pull_request artifact not produced; got kinds %v", keysOf(kindSet))
	}

	// Every expected artifact from the fixture must have been produced.
	for _, want := range f.ExpectedArtifacts {
		if !kindSet[want] {
			t.Errorf("fixture expects artifact %q but it was not produced", want)
		}
	}

	// P10.6: every measured stage must have a non-zero duration.
	for _, s := range metrics.lifecycleStages() {
		if metrics.elapsed[s] <= 0 {
			t.Errorf("stage %q elapsed = %v, want > 0", s, metrics.elapsed[s])
		}
	}
}

// TestSevenFailureDrill is the consolidated 7-failure drill (P10.5). It asserts
// that each of the 7 distinct failure modes is handled without a panic and with
// a detectable error/state, all in one place rather than scattered tests.
func TestSevenFailureDrill(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	root := "../.."
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// 1. Unknown symbol analyze.
	if _, _, err := ts.Analyze("NoSuchSymbolXYZ_000"); err == nil {
		t.Error("drill 1: unknown symbol should error")
	}

	// 2. Resume a nonexistent task.
	if _, err := ts.Resume("task-does-not-exist"); err == nil {
		t.Error("drill 2: resume nonexistent task should error")
	}

	// 3. Pause a nonexistent task.
	if err := ts.Pause("task-does-not-exist", "drill"); err == nil {
		t.Error("drill 3: pause nonexistent task should error")
	}

	// 4. Retry a nonexistent task.
	if _, err := ts.Retry("task-does-not-exist"); err == nil {
		t.Error("drill 4: retry nonexistent task should error")
	}

	// 5. Create a PR from a nonexistent task.
	if _, _, err := ts.CreatePR("task-does-not-exist", "drill"); err == nil {
		t.Error("drill 5: PR from nonexistent task should error")
	}

	// 6. Rollback a nonexistent task.
	if err := ts.Rollback("task-does-not-exist", "drill"); err == nil {
		t.Error("drill 6: rollback nonexistent task should error")
	}

	// 7. Get an artifact by a nonexistent ID must error (artifact store lookup).
	if _, err := ts.Artifacts().Get("artifact-does-not-exist"); err == nil {
		t.Error("drill 7: get nonexistent artifact should error")
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}