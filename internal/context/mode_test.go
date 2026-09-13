package context

import "testing"

func TestModeFor(t *testing.T) {
	modes := DefaultModes()
	if len(modes) != 5 {
		t.Fatalf("DefaultModes() = %d, want 5", len(modes))
	}
	seen := map[string]bool{}
	for _, m := range modes {
		if seen[m.Name] {
			t.Errorf("duplicate mode %q", m.Name)
		}
		seen[m.Name] = true
		if PolicyFor(m.TaskType).MaxTokens <= 0 {
			t.Errorf("mode %q task type %q has no policy", m.Name, m.TaskType)
		}
	}
	for _, name := range []string{ModeFix, ModeReview, ModeArchitecture, ModeIncident, ModeExplain} {
		if _, ok := ModeFor(name); !ok {
			t.Errorf("ModeFor(%q) missing", name)
		}
	}
	if _, ok := ModeFor("nonsense"); ok {
		t.Error("ModeFor(unknown) = ok, want false")
	}
}

func TestModeNames(t *testing.T) {
	names := ModeNames()
	want := []string{ModeFix, ModeReview, ModeArchitecture, ModeIncident, ModeExplain}
	if len(names) != len(want) {
		t.Fatalf("ModeNames() = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("ModeNames()[%d] = %q, want %q", i, names[i], n)
		}
	}
}

func TestOrchestrateModeOverridesPolicy(t *testing.T) {
	e := testEngine(t)
	// "HandleUsers" classifies as add_feature; the explain mode must force the
	// documentation policy family and the l1 disclosure level.
	res, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Mode: ModeExplain})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if res.Mode != ModeExplain {
		t.Errorf("mode = %q, want %q", res.Mode, ModeExplain)
	}
	if res.TaskType != TaskDocumentation {
		t.Errorf("task type = %s, want %s (explain mode policy)", res.TaskType, TaskDocumentation)
	}
	if res.Plan.TaskType != TaskDocumentation {
		t.Errorf("plan task type = %s, want %s", res.Plan.TaskType, TaskDocumentation)
	}
	if res.Plan.Policy.RetrievalLevel != "l1" {
		t.Errorf("retrieval level = %q, want l1 (explain mode)", res.Plan.Policy.RetrievalLevel)
	}
	if !sameStageOrder(res.Events) {
		t.Errorf("stage order changed by mode: %v", res.Events)
	}
}

func TestOrchestrateUnknownMode(t *testing.T) {
	e := testEngine(t)
	if _, err := e.Orchestrate("HandleUsers", OrchestrateOptions{Mode: "nonsense"}); err == nil {
		t.Fatal("unknown mode: want error, got nil")
	}
}

func sameStageOrder(events []OrchestrateStage) bool {
	want := []OrchestrateStage{
		StageTaskReceived, StageTaskClassified, StagePlanCreated,
		StageEvidenceSelected, StageBudgetApplied, StageContextDelivered,
	}
	if len(events) != len(want) {
		return false
	}
	for i, w := range want {
		if events[i] != w {
			return false
		}
	}
	return true
}
