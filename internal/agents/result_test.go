package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/evidence"
)

// TestAgentResultRequiredFields verifies that a constructed result carries all
// the contract fields (task_id, agent, status, result, evidence,
// risks, confidence, artifacts, recommended_action).
func TestAgentResultRequiredFields(t *testing.T) {
	r := NewAgentResult("task-1", "coder-a").
		WithResult("implemented x").
		WithConfidence(0.9).
		AddEvidence(evidence.FromGraphImpact("pkg/x", []string{"pkg/y"})).
		AddRisk(domain.Risk{Level: domain.RiskMedium, Score: 0.5}).
		AddArtifact("patch-1").
		WithRecommendedAction("run tests")

	if r.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want task-1", r.TaskID)
	}
	if r.Agent != "coder-a" {
		t.Errorf("Agent = %q, want coder-a", r.Agent)
	}
	if r.Status != ResultSuccess {
		t.Errorf("Status = %q, want success", r.Status)
	}
	if r.Result == "" {
		t.Error("Result should be set")
	}
	if len(r.Evidence) != 1 {
		t.Errorf("len(Evidence) = %d, want 1", len(r.Evidence))
	}
	if len(r.Risks) != 1 {
		t.Errorf("len(Risks) = %d, want 1", len(r.Risks))
	}
	if r.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", r.Confidence)
	}
	if len(r.Artifacts) != 1 {
		t.Errorf("len(Artifacts) = %d, want 1", len(r.Artifacts))
	}
	if r.RecommendedAction != "run tests" {
		t.Errorf("RecommendedAction = %q, want 'run tests'", r.RecommendedAction)
	}
}

// TestAgentResultSerializationRoundTrip verifies the result survives a
// JSON marshal/unmarshal cycle with all fields intact.
func TestAgentResultSerializationRoundTrip(t *testing.T) {
	orig := NewAgentResult("task-2", "reviewer-b").
		WithStatus(ResultFailure).
		WithResult("found a defect").
		WithConfidence(0.7).
		AddEvidence(evidence.FromTestResult("pkg/x", false, "TestX failed")).
		AddRisk(domain.Risk{Level: domain.RiskMedium, Factors: []string{"unverified"}, Score: 0.4}).
		AddArtifact("review-1").
		WithRecommendedAction("fix TestX")

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got AgentResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.TaskID != orig.TaskID || got.Agent != orig.Agent || got.Status != orig.Status || got.Result != orig.Result {
		t.Errorf("scalar fields lost in round-trip: %+v", got)
	}
	if got.Confidence != orig.Confidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, orig.Confidence)
	}
	if len(got.Evidence) != len(orig.Evidence) {
		t.Errorf("len(Evidence) = %d, want %d", len(got.Evidence), len(orig.Evidence))
	}
	if len(got.Risks) != len(orig.Risks) {
		t.Errorf("len(Risks) = %d, want %d", len(got.Risks), len(orig.Risks))
	}
	if len(got.Artifacts) != len(orig.Artifacts) {
		t.Errorf("len(Artifacts) = %d, want %d", len(got.Artifacts), len(orig.Artifacts))
	}
	if got.RecommendedAction != orig.RecommendedAction {
		t.Errorf("RecommendedAction = %q, want %q", got.RecommendedAction, orig.RecommendedAction)
	}
	// The JSON must carry the field names per the spec.
	for _, want := range []string{"task_id", "agent", "status", "result", "evidence", "risks", "confidence", "artifacts", "recommended_action"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON missing field %q", want)
		}
	}
}

// TestAgentResultConfidenceRange verifies confidence is clamped to [0,1].
func TestAgentResultConfidenceRange(t *testing.T) {
	if got := NewAgentResult("t", "a").WithConfidence(1.5).Confidence; got != 1.0 {
		t.Errorf("Confidence above 1 -> %v, want 1.0", got)
	}
	if got := NewAgentResult("t", "a").WithConfidence(-0.5).Confidence; got != 0.0 {
		t.Errorf("Confidence below 0 -> %v, want 0.0", got)
	}
	if got := NewAgentResult("t", "a").WithConfidence(0.42).Confidence; got != 0.42 {
		t.Errorf("Confidence in range -> %v, want 0.42", got)
	}
}

// TestAgentResultEmptyArrayDefaults verifies a freshly constructed result
// serializes its slice fields as empty arrays, not null.
func TestAgentResultEmptyArrayDefaults(t *testing.T) {
	r := NewAgentResult("t", "a")
	if r.Evidence == nil || r.Risks == nil || r.Artifacts == nil {
		t.Fatal("constructor should leave slices non-nil (empty)")
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, `"evidence":null`) {
		t.Errorf("evidence serialized as null: %s", s)
	}
	if strings.Contains(s, `"risks":null`) {
		t.Errorf("risks serialized as null: %s", s)
	}
	if strings.Contains(s, `"artifacts":null`) {
		t.Errorf("artifacts serialized as null: %s", s)
	}
}

// TestProduceResultWiresSpecialist verifies the specialist execution path builds
// a result contract whose Agent matches the specialist identity.
func TestProduceResultWiresSpecialist(t *testing.T) {
	s := NewSpecialist(RoleCoder, "coder-a")
	r := s.ProduceResult("task-9")
	if r.Agent != "coder-a" {
		t.Errorf("Agent = %q, want coder-a", r.Agent)
	}
	if r.TaskID != "task-9" {
		t.Errorf("TaskID = %q, want task-9", r.TaskID)
	}
	if len(r.Evidence) != 0 || len(r.Risks) != 0 || len(r.Artifacts) != 0 {
		t.Error("freshly produced result should start empty")
	}
}
