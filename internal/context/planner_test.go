package context

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestClassifyTask(t *testing.T) {
	cases := []struct {
		intent string
		want   TaskType
	}{
		{"fix the auth panic", TaskFixBug},
		{"security review of auth", TaskSecurityReview},
		{"document the dispatch package", TaskDocumentation},
		{"the build fails with a compile error", TaskBuildFailure},
		{"refactor the planner", TaskRefactor},
		{"add support for sqlite", TaskAddFeature},
		{"the login is slow", TaskPerformanceReview},
		{"unrelated gibberish", TaskAddFeature},
	}
	for _, c := range cases {
		if got := ClassifyTask(c.intent); got != c.want {
			t.Errorf("ClassifyTask(%q) = %s, want %s", c.intent, got, c.want)
		}
	}
}

func TestDefaultPolicies(t *testing.T) {
	policies := DefaultPolicies()
	if len(policies) != 7 {
		t.Fatalf("DefaultPolicies() = %d entries, want 7", len(policies))
	}
	seen := map[TaskType]bool{}
	for _, p := range policies {
		if seen[p.Type] {
			t.Errorf("duplicate policy for %s", p.Type)
		}
		seen[p.Type] = true
		if len(p.Priorities) != 7 {
			t.Errorf("policy %s has %d priorities, want 7", p.Type, len(p.Priorities))
		}
		if p.MaxTokens <= 0 {
			t.Errorf("policy %s MaxTokens = %d, want > 0", p.Type, p.MaxTokens)
		}
	}
	// Every TaskType constant has a policy.
	for _, tt := range []TaskType{TaskFixBug, TaskBuildFailure, TaskRefactor, TaskAddFeature, TaskSecurityReview, TaskPerformanceReview, TaskDocumentation} {
		if !seen[tt] {
			t.Errorf("missing policy for %s", tt)
		}
	}
}

func TestSelectEvidence(t *testing.T) {
	pkt := &domain.ContextPacket{
		Facts: []domain.Claim{
			{Statement: "graph fact", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
			{Statement: "test fact", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
			{Statement: "policy fact", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
			{Statement: "unknown type fact", Evidence: []domain.Evidence{{Type: domain.EvidenceType("mystery")}}},
			{Statement: "a very long statement " + strings.Repeat("x", 500), Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		},
	}
	policy := PolicyFor(TaskFixBug) // graph 1.0, test 0.9, policy 0.3
	sels := SelectEvidence(pkt, policy)

	// Ranking: weight DESC, then tokens ASC. graph(1.0) first; among the two
	// graph facts the shorter statement wins; unknown type last at weight 0.
	if len(sels) != 5 {
		t.Fatalf("SelectEvidence = %d selections, want 5", len(sels))
	}
	if sels[0].Type != domain.EvidenceGraph || sels[0].Weight != 1.0 {
		t.Errorf("first selection = %s(%g), want graph(1)", sels[0].Type, sels[0].Weight)
	}
	if sels[1].Type != domain.EvidenceGraph {
		t.Errorf("second selection = %s, want graph (shortest same-weight first)", sels[1].Type)
	}
	if sels[2].Type != domain.EvidenceTest {
		t.Errorf("third selection = %s, want test", sels[2].Type)
	}
	if sels[3].Type != domain.EvidencePolicy {
		t.Errorf("fourth selection = %s, want policy", sels[3].Type)
	}
	if sels[4].Type != domain.EvidenceType("mystery") || sels[4].Weight != 0 {
		t.Errorf("last selection = %s(%g), want mystery(0)", sels[4].Type, sels[4].Weight)
	}
	if !strings.Contains(sels[4].Reason, "no policy priority for mystery") {
		t.Errorf("weight-0 reason = %q, want 'no policy priority for mystery'", sels[4].Reason)
	}
	// Content capped at 200 runes.
	if len([]rune(sels[1].Content)) > 200 {
		t.Errorf("content not capped at 200 runes: %d", len([]rune(sels[1].Content)))
	}
	// Reason format for scored items.
	if !strings.Contains(sels[0].Reason, "evidence type graph scored 1 for fix_bug") {
		t.Errorf("scored reason = %q, want 'evidence type graph scored 1 for fix_bug'", sels[0].Reason)
	}
}

func TestFitToBudget(t *testing.T) {
	// Case A: 10 same-type selections of 10 tokens each, budget 30 → exactly
	// 3 kept (only the first is a diversity anchor), truncated.
	sels := make([]Selection, 10)
	for i := range sels {
		sels[i] = Selection{Type: domain.EvidenceGraph, Tokens: 10}
	}
	kept, truncated := FitToBudget(sels, 30)
	if len(kept) != 3 {
		t.Errorf("budget 30 with 10x10tok: kept %d, want 3", len(kept))
	}
	if !truncated {
		t.Error("expected truncated=true when items were dropped")
	}

	// Case B: diversity keeps the first of a new type even when it pushes over
	// budget, while a later same-type item that would overflow is dropped.
	// Items: graph(10) test(10) build(10) memory(5) graph(10), budget 30.
	sels = []Selection{
		{Type: domain.EvidenceGraph, Tokens: 10},
		{Type: domain.EvidenceTest, Tokens: 10},
		{Type: domain.EvidenceBuild, Tokens: 10},
		{Type: domain.EvidenceMemory, Tokens: 5},
		{Type: domain.EvidenceGraph, Tokens: 10},
	}
	kept, truncated = FitToBudget(sels, 30)
	if len(kept) != 4 {
		t.Errorf("diversity case: kept %d, want 4 (memory anchor kept over budget, trailing graph dropped)", len(kept))
	}
	if kept[3].Type != domain.EvidenceMemory {
		t.Errorf("diversity case: last kept = %s, want memory anchor", kept[3].Type)
	}
	if !truncated {
		t.Error("diversity case: expected truncated=true (trailing graph dropped)")
	}

	// Case C: budget <= 0 keeps everything, never truncated.
	all, truncated := FitToBudget(sels, 0)
	if len(all) != 5 || truncated {
		t.Errorf("budget 0: kept %d truncated=%v, want all 5 kept, not truncated", len(all), truncated)
	}
}

func TestPlanPacket(t *testing.T) {
	pkt := &domain.ContextPacket{
		Task: "test task",
		Facts: []domain.Claim{
			{Statement: "graph fact", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		},
	}
	// Budget 0 → policy MaxTokens (fix_bug = 8000).
	plan := PlanPacket(pkt, "fix the auth panic", 0)
	if plan.TaskType != TaskFixBug {
		t.Errorf("PlanPacket task type = %s, want fix_bug", plan.TaskType)
	}
	if plan.Budget != 8000 {
		t.Errorf("PlanPacket budget = %d, want 8000 (policy MaxTokens fallback)", plan.Budget)
	}
	// Envelope version stamping on the packet.
	if pkt.EnvelopeVersion != domain.EnvelopeVersionV1 {
		t.Errorf("EnvelopeVersion = %d, want %d", pkt.EnvelopeVersion, domain.EnvelopeVersionV1)
	}
	if pkt.SchemaVersion != "1.0.0" {
		t.Errorf("SchemaVersion = %q, want 1.0.0", pkt.SchemaVersion)
	}
	// Pre-set SchemaVersion is left alone.
	pkt2 := &domain.ContextPacket{SchemaVersion: "9.9.9"}
	PlanPacket(pkt2, "refactor the planner", 0)
	if pkt2.SchemaVersion != "9.9.9" {
		t.Errorf("existing SchemaVersion overwritten: %q", pkt2.SchemaVersion)
	}
}

func TestRenderPlan(t *testing.T) {
	pkt := &domain.ContextPacket{
		Facts: []domain.Claim{
			{Statement: "graph fact", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
			{Statement: "test fact", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
		},
	}
	plan := PlanPacket(pkt, "fix the auth panic", 1000)
	out := RenderPlan(plan)
	if out == "" {
		t.Fatal("RenderPlan returned empty string")
	}
	if !strings.Contains(out, "fix_bug") {
		t.Errorf("RenderPlan missing task type: %q", out)
	}
	if !strings.Contains(out, "evidence type") {
		t.Errorf("RenderPlan missing reason string: %q", out)
	}
}

func TestRetrievalLevelFor(t *testing.T) {
	cases := []struct {
		task TaskType
		want string
	}{
		{TaskDocumentation, "l1"},
		{TaskAddFeature, "l2"},
		{TaskFixBug, "l2"},
		{TaskBuildFailure, "l2"},
		{TaskSecurityReview, "l2"},
		{TaskPerformanceReview, "l2"},
		{TaskRefactor, "l3"},
		// Unknown types fall back to the fix_bug policy (l2).
		{TaskType("no_such_type"), "l2"},
	}
	for _, c := range cases {
		if got := RetrievalLevelFor(c.task); got != c.want {
			t.Errorf("RetrievalLevelFor(%s) = %q, want %q", c.task, got, c.want)
		}
	}
}
