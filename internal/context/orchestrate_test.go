package context

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestOrchestrateRequiresIntent(t *testing.T) {
	e := testEngine(t)
	if _, err := e.Orchestrate("", OrchestrateOptions{}); err == nil {
		t.Fatal("Orchestrate with empty intent: want error, got nil")
	}
}

func TestOrchestrateStageOrder(t *testing.T) {
	e := testEngine(t)
	res, err := e.Orchestrate("HandleUsers", OrchestrateOptions{})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	want := []OrchestrateStage{
		StageTaskReceived,
		StageTaskClassified,
		StagePlanCreated,
		StageEvidenceSelected,
		StageBudgetApplied,
		StageContextDelivered,
	}
	if !reflect.DeepEqual(res.Events, want) {
		t.Errorf("stage order = %v, want %v", res.Events, want)
	}
	// The classified task type must be a real policy-backed family.
	if res.TaskType == "" || PolicyFor(res.TaskType).MaxTokens <= 0 {
		t.Errorf("resolved task type %q has no policy", res.TaskType)
	}
}

func TestOrchestrateEmitsAllStages(t *testing.T) {
	e := testEngine(t)
	bus := eventbus.New()
	kinds := []eventbus.Kind{
		eventbus.TaskReceived,
		eventbus.TaskClassified,
		eventbus.PlanProduced,
		eventbus.EvidenceSelected,
		eventbus.BudgetApplied,
		eventbus.ContextDelivered,
	}
	got := make(chan eventbus.Kind, 16)
	for _, k := range kinds {
		k := k
		bus.Subscribe(k, func(ev eventbus.Event) { got <- ev.Kind })
	}
	e.WithBus(bus)

	if _, err := e.Orchestrate("HandleUsers", OrchestrateOptions{}); err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	seen := map[eventbus.Kind]bool{}
	for i := 0; i < len(kinds); i++ {
		select {
		case k := <-got:
			seen[k] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for stage events; saw %d of %d", len(seen), len(kinds))
		}
	}
	for _, k := range kinds {
		if !seen[k] {
			t.Errorf("bus never delivered %s", k)
		}
	}
}

func TestOrchestrateDeterministic(t *testing.T) {
	e := testEngine(t)
	r1, err := e.Orchestrate("HandleUsers", OrchestrateOptions{})
	if err != nil {
		t.Fatalf("Orchestrate #1: %v", err)
	}
	r2, err := e.Orchestrate("HandleUsers", OrchestrateOptions{})
	if err != nil {
		t.Fatalf("Orchestrate #2: %v", err)
	}
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Errorf("determinism violated:\n run1: %s\n run2: %s", b1, b2)
	}
}

func TestOrchestrateEnvelopeAndHandle(t *testing.T) {
	e := testEngine(t)
	res, err := e.Orchestrate("HandleUsers", OrchestrateOptions{})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if res.EnvelopeVersion != 1 || res.SchemaVersion != "1.0.0" {
		t.Errorf("envelope = v%d schema %q, want v1 1.0.0", res.EnvelopeVersion, res.SchemaVersion)
	}
	if res.Handle == nil {
		t.Fatal("expected an escalation handle")
	}
	if res.Handle.Type != "envelope" {
		t.Errorf("handle type = %q, want envelope", res.Handle.Type)
	}
	if res.Handle.ContentHash == "" || res.Handle.ID == "" {
		t.Errorf("handle must carry a content hash and id: %+v", res.Handle)
	}
	if res.TokenCount <= 0 {
		t.Errorf("token count = %d, want > 0", res.TokenCount)
	}
	// A nil-bus engine records stages without publishing; the engine used here
	// has no bus, so Events must still be populated (already asserted in
	// TestOrchestrateStageOrder).
	if len(res.Plan.Selections) == 0 {
		t.Error("plan selected no evidence")
	}
}

func TestOrchestrateRespectsBudget(t *testing.T) {
	e := testEngine(t)
	defaultRes, err := e.Orchestrate("HandleUsers", OrchestrateOptions{})
	if err != nil {
		t.Fatalf("Orchestrate (default budget): %v", err)
	}
	tinyRes, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Budget: 50})
	if err != nil {
		t.Fatalf("Orchestrate (tiny budget): %v", err)
	}
	if tinyRes.Budget != 50 || tinyRes.Plan.Budget != 50 {
		t.Errorf("budget not passed through: result=%d plan=%d, want 50", tinyRes.Budget, tinyRes.Plan.Budget)
	}
	// A tighter budget must never select more evidence than the default.
	if len(tinyRes.Plan.Selections) > len(defaultRes.Plan.Selections) {
		t.Errorf("tiny budget selected %d items, default selected %d — budget must not increase selection",
			len(tinyRes.Plan.Selections), len(defaultRes.Plan.Selections))
	}
	if tinyRes.Plan.TotalTokens > defaultRes.Plan.TotalTokens {
		t.Errorf("tiny budget total %d exceeds default total %d", tinyRes.Plan.TotalTokens, defaultRes.Plan.TotalTokens)
	}
}
func TestOrchestrateAppendsSkill(t *testing.T) {
	e := testEngine(t)
	res, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Skill: "kern-safe-change"})
	if err != nil {
		t.Fatalf("Orchestrate with skill: %v", err)
	}
	if res.Skill != "kern-safe-change" {
		t.Errorf("result skill = %q, want kern-safe-change", res.Skill)
	}
	// The runbook body must appear in the rendered deliverable (which feeds
	// the content hash), not just in the result metadata.
	rendered := RenderPlan(res.Plan)
	if res.Skill != "" && !strings.Contains(res.FittedText+rendered, "kern-safe-change") {
		t.Error("skill section missing from delivered render")
	}
}

func TestOrchestrateUnknownSkill(t *testing.T) {
	e := testEngine(t)
	_, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Skill: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(err.Error(), "unknown skill") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOrchestrateSkillDeterministic(t *testing.T) {
	e := testEngine(t)
	a, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Skill: "kern-safe-change"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Skill: "kern-safe-change"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Handle.ContentHash != b.Handle.ContentHash {
		t.Error("skill runbook must be covered by the content hash (deterministic)")
	}
}
