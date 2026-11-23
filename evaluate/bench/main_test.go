package main

import (
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/docsearch"
)

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

// TestFixtureTaskMatrixNonDegenerate guards the fixture matrix: every
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

// TestTaskClassMetricsProduceMetricSet asserts the harness produces
// the full per-task-class metric set for EVERY task class across EVERY
// fixture (2026-11-20 remediation: was tasks[:1] — only "lookup" was ever
// reported): token reduction (estimator AND exact o200k side by side),
// tool-call reduction, retries, latency, cost, and the outcome flags
// (first-pass, verified, human intervention, regression).
func TestTaskClassMetricsProduceMetricSet(t *testing.T) {
	ms := taskClassMetrics()
	if len(ms) == 0 {
		t.Fatal("taskClassMetrics returned no rows")
	}
	if want := len(fixtures) * len(tasks); len(ms) != want {
		t.Fatalf("taskClassMetrics rows = %d, want %d (every fixture x every task class)", len(ms), want)
	}
	seenTasks := map[string]bool{}
	seenFixtures := map[string]bool{}
	for _, m := range ms {
		if m.fixture == "" || m.task == "" {
			t.Fatalf("class row missing fixture/task: %+v", m)
		}
		if m.beforeTokens == 0 || m.afterTokens == 0 {
			t.Fatalf("class row degenerate tokens: before=%d after=%d", m.beforeTokens, m.afterTokens)
		}
		// Exact o200k counts must be present (tables are embedded) and drift
		// labeled — never silently mixed into the estimator numbers.
		if m.exactBefore == 0 {
			t.Fatalf("class row %s/%s: exact o200k before-tokens = 0 (tokenizer unavailable?)", m.fixture, m.task)
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
		seenTasks[m.task] = true
		seenFixtures[m.fixture] = true
	}
	for _, tk := range tasks {
		if !seenTasks[tk.name] {
			t.Errorf("task class %q missing from taskClassMetrics", tk.name)
		}
	}
	for _, f := range fixtures {
		if !seenFixtures[f.name] {
			t.Errorf("fixture %q missing from taskClassMetrics", f.name)
		}
	}
}

// TestRecallQueriesHitTarget runs the harness's docs-retrieval recall gate
// in CI (2026-11-20 remediation): index the repo's docs and require the
// expanded recallQueries set to surface its expected file in the top-5 with
// recall >= 0.9 (the pre-expansion baseline was 2/3).
func TestRecallQueriesHitTarget(t *testing.T) {
	ix, err := docsearch.IndexDir("../..")
	if err != nil {
		t.Skipf("docs index unavailable: %v", err)
	}
	hit := 0
	for _, q := range recallQueries {
		results := ix.Search(q.query, 5)
		ok := false
		for _, r := range results {
			if strings.Contains(r.Doc.Chunk.File, q.file) {
				ok = true
				break
			}
		}
		if ok {
			hit++
		} else {
			// Log what DID come back so a miss is debuggable from the test
			// output alone (which doc outranked the expected one).
			var top []string
			for _, r := range results {
				top = append(top, r.Doc.Chunk.File)
			}
			t.Logf("MISS %-44s -> %s (top-5: %v)", q.query, q.file, top)
		}
	}
	recall := pctf(hit, len(recallQueries))
	if recall < 90 {
		t.Errorf("recall@5 = %d/%d (%.0f%%), want >= 90%%", hit, len(recallQueries), recall)
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

// TestMain pins an UNREACHABLE default provider for the whole package so
// every bare Prompt() call (no explicit --llm) deterministically falls back
// to the deterministic path. Before the fix,
// the LLM stage never ran without --llm, so tests were immune to the host
// machine; now the stage DEFAULTS to the auto chain, and on a machine with
// agent CLIs installed (claude/opencode/...) the chain would actually answer
// — replacing the deterministic output tests assert and adding seconds per
// call. Individual tests that pin their own provider via t.Setenv override
// these defaults (t.Setenv restores after each test).
func TestMain(m *testing.M) {
	_ = os.Setenv("KERN_LLM_PROVIDER", "ollama")
	_ = os.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	code := m.Run()
	_ = os.Unsetenv("KERN_LLM_PROVIDER")
	_ = os.Unsetenv("OLLAMA_HOST")
	os.Exit(code)
}
