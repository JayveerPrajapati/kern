package main

import "testing"

// TestGatesAreMet pins the benchmark's hard gates so a regression in any
// compression surface fails CI instead of silently shipping weaker numbers.
func TestGatesAreMet(t *testing.T) {
	failures := checkGates(runMetrics())
	if len(failures) > 0 {
		t.Fatalf("bench gates failed: %v", failures)
	}
}

// TestSamplesAreNonDegenerate guards against accidentally replacing the corpus
// with inputs that produce no measurable output (the harness only measures
// line-structured inputs honestly).
func TestSamplesAreNonDegenerate(t *testing.T) {
	for _, m := range runMetrics() {
		if m.raw == "" || m.out == "" {
			t.Fatalf("sample %s degenerated (raw=%d out=%d)", m.name, len(m.raw), len(m.out))
		}
	}
}

// TestFixtureTaskMatrixNonDegenerate guards the Phase 17 fixture matrix: every
// fixture and task const must be non-empty, the six fixture types must all be
// present, and the fixtures must be meaningfully different in size.
func TestFixtureTaskMatrixNonDegenerate(t *testing.T) {
	if len(fixtures) != 6 {
		t.Fatalf("want 6 fixtures, got %d", len(fixtures))
	}
	if len(tasks) != 8 {
		t.Fatalf("want 8 task types, got %d", len(tasks))
	}

	lineCount := func(s string) int {
		if s == "" {
			return 0
		}
		n := 1
		for _, c := range s {
			if c == '\n' {
				n++
			}
		}
		return n
	}

	lines := make([]int, len(fixtures))
	for i, f := range fixtures {
		if f.corpus == "" {
			t.Fatalf("fixture %q is empty", f.name)
		}
		lines[i] = lineCount(f.corpus)
	}
	for _, tsk := range tasks {
		if tsk.prompt == "" {
			t.Fatalf("task %q is empty", tsk.name)
		}
	}

	// The fixtures must scale: small < medium < large (by line count).
	byName := map[string]int{}
	for i, f := range fixtures {
		byName[f.name] = lines[i]
	}
	small, okS := byName["small repository"]
	medium, okM := byName["medium monolith"]
	large, okL := byName["large monorepo"]
	if !okS || !okM || !okL {
		t.Fatalf("matrix missing small/medium/large fixture names: %v", byName)
	}
	if !(small < medium && medium < large) {
		t.Fatalf("fixture sizes not strictly increasing: small=%d medium=%d large=%d", small, medium, large)
	}
}

// TestTaskClassMetricsProduceMetricSet asserts the Phase 17.3 harness produces
// the full per-task-class metric set for at least one task class: token
// reduction, tool-call reduction, retries, latency, cost, and the outcome
// flags (first-pass, verified, human intervention, regression).
func TestTaskClassMetricsProduceMetricSet(t *testing.T) {
	ms := taskClassMetrics()
	if len(ms) == 0 {
		t.Fatal("taskClassMetrics returned no rows")
	}
	m := ms[0]
	if m.fixture == "" || m.task == "" {
		t.Fatalf("class row missing fixture/task: %+v", m)
	}
	if m.beforeTokens == 0 || m.afterTokens == 0 {
		t.Fatalf("class row degenerate tokens: before=%d after=%d", m.beforeTokens, m.afterTokens)
	}
	if m.toolCalls < 0 {
		t.Fatalf("tool calls negative: %d", m.toolCalls)
	}
	if m.latencyMs < 0 {
		t.Fatalf("latency negative: %d", m.latencyMs)
	}
	if m.cost < 0 {
		t.Fatalf("cost negative: %.4f", m.cost)
	}
	// Outcomes must be boolean and consistent: verified implies first-pass.
	if m.verifiedSuccess && !m.firstPass {
		t.Fatalf("verifiedSuccess true but firstPass false: %+v", m)
	}
}

// TestMeasureClassExercisesOutcomePaths verifies the deterministic retry/regression
// outcome paths on the incident class with a retry factor.
func TestMeasureClassExercisesOutcomePaths(t *testing.T) {
	f := fixture{name: "small repository", corpus: fixtureSmallRepo}
	tk := task{name: "incident", prompt: taskIncident}
	m := measureClass(f, tk, 2)
	if m.retries != 2 {
		t.Errorf("retries = %d, want 2 with retryFactor=2", m.retries)
	}
	if m.task != "incident" {
		t.Errorf("task = %q, want incident", m.task)
	}
}
