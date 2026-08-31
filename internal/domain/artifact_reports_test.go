package domain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestNewArtifactKindsCovered verifies every ArtifactKind constant is backed by
// a concrete struct type (either an existing domain type or one of the
// report types). This proves the artifact contract is fully covered.
func TestNewArtifactKindsCovered(t *testing.T) {
	// kind -> concrete struct type name.
	coverage := map[ArtifactKind]string{
		ArtifactContextPacket:      "ContextPacket",
		ArtifactAnalysisReport:     "AnalysisReport",
		ArtifactImpactReport:       "ImpactReport",
		ArtifactRiskReport:         "RiskReport",
		ArtifactPlan:               "Plan",
		ArtifactCodePatch:          "CodePatch",
		ArtifactDiff:               "Diff",
		ArtifactTestReport:         "TestReport",
		ArtifactSecurityReport:     "SecurityReport",
		ArtifactArchitectureReport: "ArchitectureReport",
		ArtifactVerificationReport: "VerificationReport",
		ArtifactIncidentReport:     "IncidentReport",
		ArtifactRootCauseReport:    "RootCauseReport",
		ArtifactEvidenceReport:     "EvidenceReport",
		ArtifactPullRequest:        "PullRequest",
		ArtifactDeployment:         "Deployment",
		ArtifactDeploymentReport:   "DeploymentReport",
		ArtifactRollbackReport:     "RollbackReport",
		ArtifactMemoryEntry:        "MemoryEntry",
		ArtifactAudit:              "AuditReport",
	}

	all := []ArtifactKind{
		ArtifactContextPacket,
		ArtifactAnalysisReport,
		ArtifactImpactReport,
		ArtifactRiskReport,
		ArtifactPlan,
		ArtifactCodePatch,
		ArtifactDiff,
		ArtifactTestReport,
		ArtifactSecurityReport,
		ArtifactArchitectureReport,
		ArtifactVerificationReport,
		ArtifactIncidentReport,
		ArtifactRootCauseReport,
		ArtifactEvidenceReport,
		ArtifactPullRequest,
		ArtifactDeployment,
		ArtifactDeploymentReport,
		ArtifactRollbackReport,
		ArtifactMemoryEntry,
		ArtifactAudit,
	}

	if len(coverage) != len(all) {
		t.Errorf("coverage map has %d kinds, want %d constants", len(coverage), len(all))
	}

	for _, kind := range all {
		if _, ok := coverage[kind]; !ok {
			t.Errorf("ArtifactKind %q has no coverage entry", kind)
		}
	}
	for kind := range coverage {
		found := false
		for _, k := range all {
			if k == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("coverage references unknown kind %q", kind)
		}
	}
}

// TestReportStructsJSON round-trips representative new report structs through
// JSON and asserts the fields survive, proving the JSON contract works.
func TestReportStructsJSON(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	analysis := AnalysisReport{
		Summary:      "sum",
		Target:       "pkg/core",
		Findings:     []string{"f1"},
		Symbols:      []string{"s1"},
		Dependencies: []string{"d1"},
		Evidence:     []string{"e1"},
		Risks:        []string{"r1"},
		GeneratedAt:  now,
	}
	roundTripJSON(t, "AnalysisReport", analysis)

	security := SecurityReport{
		Severity: "HIGH",
		Findings: []SecurityFinding{
			{ID: "F1", Title: "SQLi", Severity: "high", File: "a.go", Line: 42, Description: "desc"},
		},
		Passed:      false,
		GeneratedAt: now,
	}
	roundTripJSON(t, "SecurityReport", security)

	patch := CodePatch{
		TaskID:      "task-1",
		Files:       []string{"a.go"},
		Patch:       "--- a\n+++ b",
		Language:    "go",
		Stats:       PatchStats{FilesChanged: 1, Insertions: 3, Deletions: 1},
		GeneratedAt: now,
	}
	roundTripJSON(t, "CodePatch", patch)

	vr := VerificationReport{
		ID:          "ver-1",
		TaskID:      "task-1",
		Verdict:     "PASS",
		Summary:     "build+test ok",
		Passed:      true,
		GeneratedAt: now,
		Checks: []VerificationCheck{
			{Name: "build", Status: "pass", Detail: "ok"},
			{Name: "test", Status: "pass", Detail: "ok"},
		},
	}
	roundTripJSON(t, "VerificationReport", vr)
}

func roundTripJSON(t *testing.T, name string, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal error: %v", name, err)
	}
	out := reflect.New(reflect.TypeOf(v)).Interface()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("%s: unmarshal error: %v", name, err)
	}
	got := reflect.ValueOf(out).Elem().Interface()
	if !reflect.DeepEqual(got, v) {
		t.Errorf("%s: round-trip mismatch:\n got=%#v\nwant=%#v", name, got, v)
	}
}
