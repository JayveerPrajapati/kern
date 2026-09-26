package learning

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// usageRecord returns one observed usage sample with deterministic fields.
func usageRecord(task, slice string, members, used int, outcome string, at time.Time) domain.ContextUsageRecord {
	return domain.ContextUsageRecord{
		Task:    task,
		Slice:   slice,
		Members: members,
		Used:    used,
		Outcome: outcome,
		At:      at,
	}
}

// TestContextUsagePatternsUnusedRecommendsShrink proves the shrink signal: a
// slice with ZERO usage across >= threshold records yields ONE RECOMMENDATION
// pattern carrying the deterministic statement, the "context:<slice>" scope,
// the record count, and the contributing task IDs as provenance.
func TestContextUsagePatternsUnusedRecommendsShrink(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ContextUsageRecord{
		usageRecord("t1", "runtime_evidence", 3, 0, "analyze", base),
		usageRecord("t2", "runtime_evidence", 2, 0, "analyze", base.Add(time.Hour)),
		usageRecord("t3", "runtime_evidence", 4, 0, "analyze", base.Add(2*time.Hour)),
	}
	patterns := ContextUsagePatterns(records, 3)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1: %+v", len(patterns), patterns)
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", p.ClaimType)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3", p.Count)
	}
	wantKey := "context:runtime_evidence"
	if p.Key != wantKey {
		t.Errorf("Key = %q, want %q", p.Key, wantKey)
	}
	wantStmt := "context slice runtime_evidence unused in 3/3 tasks — shrink or omit from future packets"
	if p.Statement != wantStmt {
		t.Errorf("Statement = %q, want %q", p.Statement, wantStmt)
	}
	if len(p.Provenance.Sources) != 3 {
		t.Errorf("Provenance.Sources = %v, want 3 sources", p.Provenance.Sources)
	}
	if p.Provenance.Latest != base.Add(2*time.Hour) {
		t.Errorf("Provenance.Latest = %v, want latest record time", p.Provenance.Latest)
	}
	if p.Created != base.Add(2*time.Hour) {
		t.Errorf("Created = %v, want newest record time", p.Created)
	}
}

// TestContextUsagePatternsPartialUseInfers proves the review signal: a slice
// used in strictly fewer than half the tasks yields ONE INFERENCE pattern with
// the deterministic "used in X of N tasks" statement.
func TestContextUsagePatternsPartialUseInfers(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ContextUsageRecord{
		usageRecord("t1", "symbols", 10, 3, "analyze", base),
		usageRecord("t2", "symbols", 12, 1, "analyze", base.Add(time.Hour)),
		usageRecord("t3", "symbols", 8, 0, "analyze", base.Add(2*time.Hour)),
		usageRecord("t4", "symbols", 9, 2, "analyze", base.Add(3*time.Hour)),
		usageRecord("t5", "symbols", 11, 0, "analyze", base.Add(4*time.Hour)),
	}
	// used/members = 6/50 = 12% (< 50%); used in 3 of 5 tasks.
	patterns := ContextUsagePatterns(records, 5)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1: %+v", len(patterns), patterns)
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", p.ClaimType)
	}
	wantStmt := "context slice symbols used in 3 of 5 tasks — review whether its budget is justified"
	if p.Statement != wantStmt {
		t.Errorf("Statement = %q, want %q", p.Statement, wantStmt)
	}
	if p.Count != 5 {
		t.Errorf("Count = %d, want 5", p.Count)
	}
}

// TestContextUsagePatternsFullyUsedNothing proves that a slice used in every
// task (or at 50%+ usage) yields NO pattern: a healthy slice is silent.
func TestContextUsagePatternsFullyUsedNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	full := []domain.ContextUsageRecord{
		usageRecord("t1", "files", 4, 4, "analyze", base),
		usageRecord("t2", "files", 3, 3, "analyze", base.Add(time.Hour)),
		usageRecord("t3", "files", 5, 5, "analyze", base.Add(2*time.Hour)),
	}
	if got := ContextUsagePatterns(full, 3); len(got) != 0 {
		t.Fatalf("fully used slice produced patterns: %+v", got)
	}
	// Exactly 50% usage is also silent (only strictly under 50% infers).
	half := []domain.ContextUsageRecord{
		usageRecord("t1", "memory", 4, 2, "analyze", base),
		usageRecord("t2", "memory", 4, 2, "analyze", base.Add(time.Hour)),
		usageRecord("t3", "memory", 4, 2, "analyze", base.Add(2*time.Hour)),
	}
	if got := ContextUsagePatterns(half, 3); len(got) != 0 {
		t.Fatalf("50%%-used slice produced patterns: %+v", got)
	}
}

// TestContextUsagePatternsDeterministic proves that identical input yields
// identical output (kinds sorted, sources deduped+sorted, stable order), so
// the extractor is safe to run repeatedly for idempotent memory upserts.
func TestContextUsagePatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mk := func() []domain.ContextUsageRecord {
		return []domain.ContextUsageRecord{
			usageRecord("t-b", "memory", 4, 0, "analyze", base.Add(time.Hour)),
			usageRecord("t-a", "files", 3, 0, "analyze", base),
			usageRecord("t-c", "memory", 5, 0, "analyze", base.Add(2*time.Hour)),
			usageRecord("t-a", "files", 2, 0, "analyze", base.Add(3*time.Hour)),
		}
	}
	// Same input, interleaved construction order: output must be identical.
	first := ContextUsagePatterns(mk(), 2)
	second := ContextUsagePatterns(mk(), 2)
	if len(first) != 2 {
		t.Fatalf("patterns = %d, want 2 (files + memory): %+v", len(first), first)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("extractor is not deterministic:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	// Ordered by scope: "context:files" before "context:memory".
	if first[0].Key != "context:files" || first[1].Key != "context:memory" {
		t.Errorf("pattern order = [%s, %s], want [context:files, context:memory]", first[0].Key, first[1].Key)
	}
	// Duplicate task IDs across records dedupe into one provenance source.
	filesPattern := first[0]
	if len(filesPattern.Provenance.Sources) != 1 {
		t.Errorf("duplicate task provenance not deduped: %v", filesPattern.Provenance.Sources)
	}
	if !strings.HasPrefix(filesPattern.Provenance.Sources[0], "task t-a (analyze)") {
		t.Errorf("provenance source = %q, want task t-a prefix", filesPattern.Provenance.Sources[0])
	}
}

// TestContextUsagePatternsBelowThresholdNothing proves that fewer than
// threshold records produce no signal yet (evidence-based promotion).
func TestContextUsagePatternsBelowThresholdNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ContextUsageRecord{
		usageRecord("t1", "files", 3, 0, "analyze", base),
		usageRecord("t2", "files", 2, 0, "analyze", base.Add(time.Hour)),
	}
	if got := ContextUsagePatterns(records, 3); len(got) != 0 {
		t.Fatalf("below-threshold slice produced patterns: %+v", got)
	}
}
