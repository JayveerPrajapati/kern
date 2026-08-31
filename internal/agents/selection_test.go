package agents

import (
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
)

func TestClassifyTask(t *testing.T) {
	tests := []struct {
		name     string
		intent   string
		taskType string
		want     TaskKind
	}{
		{"incident via taskType", "investigate outage", "incident", TaskKindIncident},
		{"incident keyword", "correlate alerts for the payment outage", "", TaskKindIncident},
		{"incident root-cause", "root-cause the latency spike", "", TaskKindIncident},
		{"incident alert", "alert: database connections exhausted", "", TaskKindIncident},
		{"modernize via taskType", "rewrite the service", "modernize", TaskKindModernization},
		{"modernize refactor", "refactor the checkout flow", "", TaskKindModernization},
		{"modernize extract", "extract a payment service", "", TaskKindModernization},
		{"modernize split-monolith", "split-monolith into services", "", TaskKindModernization},
		{"docs via taskType", "update the guide", "documentation", TaskKindDocumentation},
		{"docs keyword", "document the new API endpoint", "", TaskKindDocumentation},
		{"readme keyword", "write a README for the module", "", TaskKindDocumentation},
		{"code default", "add a retry to the http client", "", TaskKindCode},
		{"code unrecognized type", "implement pagination", "feature", TaskKindCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyTask(tt.intent, tt.taskType); got != tt.want {
				t.Errorf("ClassifyTask(%q, %q) = %v, want %v", tt.intent, tt.taskType, got, tt.want)
			}
		})
	}
}

func TestSelectPipelineStages(t *testing.T) {
	tests := []struct {
		name  string
		kind  TaskKind
		roles []Role
	}{
		{"documentation", TaskKindDocumentation, []Role{RolePlanner, RoleReviewer}},
		{"incident", TaskKindIncident, []Role{RolePlanner, RoleCoder, RoleSecurity, RoleTester, RoleSRE}},
		{"modernization", TaskKindModernization, []Role{RoleArchitect, RolePlanner, RoleReviewer}},
		{"code", TaskKindCode, []Role{RolePlanner, RoleArchitect, RoleCoder, RoleReviewer, RoleSecurity, RoleTester}},
		{"default", TaskKindDefault, []Role{RolePlanner, RoleArchitect, RoleCoder, RoleReviewer, RoleSecurity, RoleTester}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stages := SelectPipeline(tt.kind)
			if len(stages) != len(tt.roles) {
				t.Fatalf("SelectPipeline(%v) has %d stages, want %d", tt.kind, len(stages), len(tt.roles))
			}
			for i, role := range tt.roles {
				if stages[i].role != role {
					t.Errorf("stage %d role = %s, want %s", i, stages[i].role, role)
				}
			}
		})
	}
}

// TestDefaultKindMatchesDefaultStages guards backward compatibility: the
// default kind must resolve to the same 6-stage sequence as the original
// hardcoded standardStages.
func TestDefaultKindMatchesDefaultStages(t *testing.T) {
	if got, want := SelectPipeline(TaskKindDefault), DefaultStages(); !reflect.DeepEqual(got, want) {
		t.Errorf("SelectPipeline(TaskKindDefault) != DefaultStages()\n got: %v\nwant: %v", got, want)
	}
	if got, want := SelectPipeline(TaskKindCode), DefaultStages(); !reflect.DeepEqual(got, want) {
		t.Errorf("SelectPipeline(TaskKindCode) != DefaultStages()\n got: %v\nwant: %v", got, want)
	}
	if got, want := DefaultStages(), standardStages; !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultStages() != standardStages\n got: %v\nwant: %v", got, want)
	}
}

func TestPipelineForKindUsesSelectedStages(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := PipelineForKind(TaskKindDocumentation, team, runtime, nil)
	if p == nil {
		t.Fatal("PipelineForKind returned nil")
	}
	if got, want := p.stages, SelectPipeline(TaskKindDocumentation); !reflect.DeepEqual(got, want) {
		t.Errorf("PipelineForKind stages = %v, want %v", got, want)
	}
}

// TestSelectWorkflowPreservesApprovalGate guards Invariant #2 (high-risk
// execution requires approval): every task-kind-specific workflow MUST contain
// at least one step with RequiresApproval before the first execution step, so
// the WorkflowEngine path (used by TaskService.RunWorkflow) never bypasses the
// human approval gate.
func TestSelectWorkflowPreservesApprovalGate(t *testing.T) {
	kinds := []TaskKind{TaskKindCode, TaskKindDefault, TaskKindDocumentation, TaskKindIncident, TaskKindModernization}
	for _, kind := range kinds {
		wf := SelectWorkflow(kind)
		if wf.ID == "" {
			t.Fatalf("SelectWorkflow(%v) returned empty workflow ID", kind)
		}
		hasApproval := false
		firstExecIdx := -1
		approvalIdx := -1
		for i, step := range wf.Steps {
			if step.RequiresApproval {
				approvalIdx = i
				hasApproval = true
			}
			// "code", "security", "test", "sre" are execution steps; "approve"
			// itself is the gate, not an execution step.
			if step.Action == "code" || step.Action == "security" || step.Action == "test" || step.Action == "sre" {
				if firstExecIdx == -1 {
					firstExecIdx = i
				}
			}
		}
		if !hasApproval {
			t.Errorf("SelectWorkflow(%v) workflow %q has no RequiresApproval step — governance gate missing", kind, wf.ID)
			continue
		}
		if firstExecIdx != -1 && approvalIdx > firstExecIdx {
			t.Errorf("SelectWorkflow(%v) workflow %q: approval gate at step %d is AFTER first execution step at %d — approval must precede execution",
				kind, wf.ID, approvalIdx, firstExecIdx)
		}
	}
}

// TestSelectWorkflowDefaultMatchesDefaultWorkflow guards backward
// compatibility: TaskKindCode and TaskKindDefault must resolve to
// agent.DefaultWorkflow() so unclassified tasks keep v1 behavior.
func TestSelectWorkflowDefaultMatchesDefaultWorkflow(t *testing.T) {
	def := agent.DefaultWorkflow()
	for _, kind := range []TaskKind{TaskKindCode, TaskKindDefault} {
		wf := SelectWorkflow(kind)
		if !reflect.DeepEqual(wf, def) {
			t.Errorf("SelectWorkflow(%v) != agent.DefaultWorkflow()\n got: %+v\nwant: %+v", kind, wf, def)
		}
	}
}
