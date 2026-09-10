package integration

import (
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestDeterministicPlanner exercises the real deterministic planner over the
// spec's acceptance examples and a synthetic packet with mixed evidence:
// classify -> policy -> SelectEvidence -> FitToBudget, envelope stamping,
// budget respect, ranking order, and run-to-run determinism.
func TestDeterministicPlanner(t *testing.T) {
	// Classifier: the spec's acceptance examples map to the documented task
	// types. Keyword order matters (performance -> documentation -> build ->
	// fix -> security -> refactor -> feature), so "sqlite" must not trip the
	// security "sqli" keyword (word-boundary matching) and "fix ... panic"
	// must win over later classes.
	cases := []struct {
		intent string
		want   context.TaskType
	}{
		{"fix the Count panic", context.TaskFixBug},
		{"add support for sqlite", context.TaskAddFeature},
		{"review auth security", context.TaskSecurityReview},
		{"compile error breaks the build", context.TaskBuildFailure},
	}
	for _, tc := range cases {
		if got := context.ClassifyTask(tc.intent); got != tc.want {
			t.Errorf("ClassifyTask(%q) = %s, want %s", tc.intent, got, tc.want)
		}
	}

	// Synthetic packet with mixed evidence types (policy, graph, test, git).
	newPkt := func() *domain.ContextPacket {
		return &domain.ContextPacket{
			Task: "fix the Count panic",
			Facts: []domain.Claim{
				{Statement: "external egress is denied by policy", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
				{Statement: "Run depends on Count", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
				{Statement: "no unit test covers Count", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
				{Statement: "Count was last touched in commit abc1234", Evidence: []domain.Evidence{{Type: domain.EvidenceGit}}},
			},
		}
	}

	// PlanPacket: classify -> policy -> SelectEvidence -> FitToBudget.
	plan := context.PlanPacket(newPkt(), "fix the Count panic", 100)
	if plan.TaskType != context.TaskFixBug {
		t.Errorf("PlanPacket task type = %s, want fix_bug", plan.TaskType)
	}
	if plan.Budget != 100 {
		t.Errorf("PlanPacket budget = %d, want 100", plan.Budget)
	}
	if len(plan.Selections) == 0 {
		t.Fatal("PlanPacket produced no selections")
	}
	// Highest-priority evidence type for fix_bug ranks first (graph 1.0).
	if plan.Selections[0].Type != domain.EvidenceGraph {
		t.Errorf("first selection type = %s, want graph (fix_bug priority 1.0)", plan.Selections[0].Type)
	}
	// Output respects the budget (no overflow when the first-of-type anchor
	// fits, which it does here: every selection is a few tokens).
	if plan.TotalTokens > plan.Budget {
		t.Errorf("plan total tokens %d exceed budget %d", plan.TotalTokens, plan.Budget)
	}
	// Ranked by weight DESC then tokens ASC — deterministic order.
	for i := 1; i < len(plan.Selections); i++ {
		prev, cur := plan.Selections[i-1], plan.Selections[i]
		if cur.Weight > prev.Weight {
			t.Errorf("selection %d weight %g > selection %d weight %g: not weight-descending",
				i, cur.Weight, i-1, prev.Weight)
		}
		if cur.Weight == prev.Weight && cur.Tokens < prev.Tokens {
			t.Errorf("selection %d tokens %d < selection %d tokens %d: tie not token-ascending",
				i, cur.Tokens, i-1, prev.Tokens)
		}
	}

	// Envelope stamping by the planner.
	pkt := newPkt()
	context.PlanPacket(pkt, "fix the Count panic", 0)
	if pkt.EnvelopeVersion != domain.EnvelopeVersionV1 || pkt.SchemaVersion != "1.0.0" {
		t.Errorf("planner envelope stamping: version=%d schema=%q",
			pkt.EnvelopeVersion, pkt.SchemaVersion)
	}

	// Budget <= 0 falls back to the policy MaxTokens (fix_bug = 8000).
	if plan2 := context.PlanPacket(newPkt(), "fix the Count panic", 0); plan2.Budget != 8000 {
		t.Errorf("budget-0 fallback = %d, want 8000 (fix_bug policy MaxTokens)", plan2.Budget)
	}

	// Determinism: two identical runs produce identical plans.
	a := context.PlanPacket(newPkt(), "fix the Count panic", 100)
	b := context.PlanPacket(newPkt(), "fix the Count panic", 100)
	if !reflect.DeepEqual(a.Selections, b.Selections) {
		t.Error("two identical PlanPacket runs produced different selections")
	}
	if a.TaskType != b.TaskType || a.Budget != b.Budget || a.TotalTokens != b.TotalTokens {
		t.Error("two identical PlanPacket runs produced different plan aggregates")
	}
}
