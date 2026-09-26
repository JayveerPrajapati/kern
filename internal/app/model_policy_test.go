package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// modelOutcomeRecord builds one verify-outcome sample for tests.
func modelOutcomeRecord(task, kind, model string, passed bool, at time.Time, cost float64) domain.ModelOutcomeRecord {
	return domain.ModelOutcomeRecord{Task: task, Kind: kind, Model: model, Passed: passed, EstCost: cost, At: at}
}

// TestModelPolicyPatternsCheapestWithinThreshold proves the "cheapest within
// threshold" rule: two models of the same kind both clear the 80% pass
// threshold, but only the LOWEST-EstCost one becomes a RECOMMENDATION — the
// pricier within-threshold model contributes nothing.
func TestModelPolicyPatternsCheapestWithinThreshold(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var records []domain.ModelOutcomeRecord
	// model-cheap: 10/10 passes, est cost 0.5 each → 5.0 total.
	for i := 0; i < 10; i++ {
		records = append(records, modelOutcomeRecord(
			"t-cheap-"+string(rune('a'+i)), "code", "model-cheap", true, base.Add(time.Duration(i)*time.Minute), 0.5))
	}
	// model-pricey: 9/10 passes (90% >= 80, within threshold), est cost 2.0
	// each → 18.0 total, pricier than model-cheap.
	for i := 0; i < 10; i++ {
		records = append(records, modelOutcomeRecord(
			"t-pricey-"+string(rune('a'+i)), "code", "model-pricey", i != 0, base.Add(time.Duration(i)*time.Minute), 2.0))
	}

	patterns := ModelPolicyPatterns(records, DefaultModelMinSamples, DefaultModelPassThreshold)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want exactly 1 (only the cheapest within-threshold model)", len(patterns))
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", p.ClaimType)
	}
	if p.Key != "model:code:model-cheap" || p.Scopes[0] != "model:code:model-cheap" {
		t.Errorf("Key/Scope = %q, want model:code:model-cheap", p.Key)
	}
	if p.Count != 10 {
		t.Errorf("Count = %d, want 10", p.Count)
	}
	if !strings.Contains(p.Statement, "for code tasks, model model-cheap passes verify 100% (10 runs, est $5.0000) — cheapest within threshold; consider defaulting") {
		t.Errorf("Statement = %q, want the cheapest-within-threshold statement", p.Statement)
	}
	if strings.Contains(p.Statement, "model-pricey") {
		t.Errorf("Statement names the pricier model: %q", p.Statement)
	}
}

// TestModelPolicyPatternsBelowThresholdInfers proves a (kind, model) group
// with >= minSamples records but a pass rate below the threshold becomes an
// INFERENCE "review selection" pattern.
func TestModelPolicyPatternsBelowThresholdInfers(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var records []domain.ModelOutcomeRecord
	// 6/10 passes = 60% < 80%.
	for i := 0; i < 10; i++ {
		records = append(records, modelOutcomeRecord(
			"t-doc-"+string(rune('a'+i)), "documentation", "model-weak", i < 6, base.Add(time.Duration(i)*time.Minute), 0))
	}

	patterns := ModelPolicyPatterns(records, DefaultModelMinSamples, DefaultModelPassThreshold)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1", len(patterns))
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", p.ClaimType)
	}
	if p.Key != "model:documentation:model-weak" {
		t.Errorf("Key = %q, want model:documentation:model-weak", p.Key)
	}
	want := "model model-weak for documentation tasks passes verify 60% (10 runs) — below the 80% threshold; review selection"
	if p.Statement != want {
		t.Errorf("Statement = %q, want %q", p.Statement, want)
	}
	if len(p.Provenance.Sources) != 10 {
		t.Errorf("provenance sources = %d, want 10 deduped task IDs", len(p.Provenance.Sources))
	}
}

// TestModelPolicyPatternsBelowMinSamplesNothing proves below-minSamples
// groups contribute no patterns.
func TestModelPolicyPatternsBelowMinSamplesNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var records []domain.ModelOutcomeRecord
	for i := 0; i < DefaultModelMinSamples-1; i++ {
		records = append(records, modelOutcomeRecord(
			"t-few-"+string(rune('a'+i)), "code", "model-few", true, base.Add(time.Duration(i)*time.Minute), 0))
	}
	if patterns := ModelPolicyPatterns(records, DefaultModelMinSamples, DefaultModelPassThreshold); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (below minSamples)", len(patterns))
	}
}

// TestModelPolicyPatternsDeterministic proves determinism two ways: the
// integer pass rate is computed with integer arithmetic rounded DOWN (7/9 →
// 77%, not 78), and the same input yields byte-identical patterns regardless
// of input ordering (the EstCost fold is task-ID-ordered, so shuffled input
// cannot change the winner or the totals).
func TestModelPolicyPatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mk := func() []domain.ModelOutcomeRecord {
		var records []domain.ModelOutcomeRecord
		// 7 passed of 9 → 7*100/9 = 77 (rounded down), below 80 → INFERENCE.
		for i := 0; i < 9; i++ {
			records = append(records, modelOutcomeRecord(
				"t-det-"+string(rune('a'+i)), "incident", "model-det", i < 7, base.Add(time.Duration(i)*time.Minute), 0.1))
		}
		return records
	}
	ordered := mk()
	shuffled := []domain.ModelOutcomeRecord{
		ordered[8], ordered[3], ordered[0], ordered[6], ordered[1],
		ordered[5], ordered[2], ordered[7], ordered[4],
	}

	pa := ModelPolicyPatterns(ordered, DefaultModelMinSamples, DefaultModelPassThreshold)
	pb := ModelPolicyPatterns(shuffled, DefaultModelMinSamples, DefaultModelPassThreshold)

	if len(pa) != 1 {
		t.Fatalf("patterns = %d, want 1", len(pa))
	}
	if pa[0].ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE (7/9 = 77%% < 80%%)", pa[0].ClaimType)
	}
	if !strings.Contains(pa[0].Statement, "passes verify 77% (9 runs)") {
		t.Errorf("Statement = %q, want integer percent 77 (rounded down)", pa[0].Statement)
	}
	if !reflect.DeepEqual(pa, pb) {
		t.Errorf("shuffled input changed the output:\nordered:   %+v\nshuffled:  %+v", pa, pb)
	}
}

// TestRecordModelOutcomesWritesRecommendation proves the full learning pass:
// enough passing verify outcomes for one (kind, model) pair write exactly one
// RECOMMENDATION typed-claim memory through the learning path (the
// "cheapest within threshold" statement, "model:<kind>:<model>" scope), and a
// second identical batch upserts the same scope instead of duplicating
// (idempotent).
func TestRecordModelOutcomesWritesRecommendation(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := make([]domain.ModelOutcomeRecord, 0, DefaultModelMinSamples)
	for i := 0; i < DefaultModelMinSamples; i++ {
		records = append(records, modelOutcomeRecord(
			"t-rec-"+string(rune('a'+i)), "code", "model-a", true, base.Add(time.Duration(i)*time.Hour), 0))
	}
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)
	n, err := RecordModelOutcomes(records, mem, DefaultModelMinSamples, DefaultModelPassThreshold)
	if err != nil {
		t.Fatalf("RecordModelOutcomes: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", m.ClaimType)
	}
	if m.Scope != "model:code:model-a" {
		t.Errorf("Scope = %q, want model:code:model-a", m.Scope)
	}
	if !strings.Contains(m.Content, "for code tasks, model model-a passes verify 100% (5 runs, est $0.0000) — cheapest within threshold; consider defaulting") {
		t.Errorf("Content = %q, want the cheapest-within-threshold statement", m.Content)
	}
	if !strings.Contains(m.Provenance, "t-rec-a") {
		t.Errorf("Provenance = %q, want the contributing task IDs", m.Provenance)
	}
	// Second identical batch: accumulator grows to 10 records for the pair,
	// so the same scope is upserted — no duplicate memory.
	n2, err := RecordModelOutcomes(records, mem, DefaultModelMinSamples, DefaultModelPassThreshold)
	if err != nil {
		t.Fatalf("RecordModelOutcomes (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1 (upsert)", n2)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories (2nd) = %d, want 1 (idempotent upsert, no duplicate)", len(mems))
	}
}

// TestRecordModelOutcomesNilGuard proves a nil memory store is a no-op that
// never panics (0, nil) — unwired paths keep their zero behavior change.
func TestRecordModelOutcomesNilGuard(t *testing.T) {
	records := []domain.ModelOutcomeRecord{
		{Task: "t-1", Kind: "code", Model: "model-a", Passed: true, At: time.Now()},
	}
	n, err := RecordModelOutcomes(records, nil, DefaultModelMinSamples, DefaultModelPassThreshold)
	if err != nil {
		t.Fatalf("nil mem returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("nil mem written = %d, want 0", n)
	}
}

// TestRecordModelOutcomeHelperNilGuard proves the verify choke-point helper
// is nil-guarded: a nil task, a nil platform, or a platform without a memory
// store are all silent no-ops that never panic.
func TestRecordModelOutcomeHelperNilGuard(t *testing.T) {
	if err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = r.(error)
			}
		}()
		(&TaskService{}).recordModelOutcome(nil, true)
		(&TaskService{platform: &Platform{}}).recordModelOutcome(nil, true)
		(&TaskService{platform: &Platform{}}).recordModelOutcome(agentTaskForTest("t-1", "verify", "analyze"), true)
		return nil
	}(); err != nil {
		t.Fatalf("recordModelOutcome panicked: %v", err)
	}
}

// agentTaskForTest builds a minimal agent.Task for helper-level tests.
func agentTaskForTest(id, input, taskType string) *agent.Task {
	task := agent.NewTask(taskType, input)
	task.ID = id
	return task
}

// TestModelPolicyKind proves the TaskKind → canonical string mapping used at
// the verify choke point.
func TestModelPolicyKind(t *testing.T) {
	cases := []struct {
		kind agents.TaskKind
		want string
	}{
		{agents.TaskKindCode, "code"},
		{agents.TaskKindDocumentation, "documentation"},
		{agents.TaskKindIncident, "incident"},
		{agents.TaskKindModernization, "modernization"},
		{agents.TaskKindDefault, "default"},
	}
	for _, c := range cases {
		if got := modelPolicyKind(c.kind); got != c.want {
			t.Errorf("modelPolicyKind(%v) = %q, want %q", c.kind, got, c.want)
		}
	}
	// The classifier's deterministic default: a standalone Verify task
	// carries only the "verify" intent, so it classifies as code.
	if got := modelPolicyKind(agents.ClassifyTask("verify", "")); got != "code" {
		t.Errorf("ClassifyTask(verify) kind = %q, want code", got)
	}
}
