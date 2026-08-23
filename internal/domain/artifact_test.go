package domain

import (
	"testing"
	"time"
)

func TestNewArtifact(t *testing.T) {
	a := NewArtifact(ArtifactPlan, "task-1", "artifacts/plan-1.md")

	if a.Kind != ArtifactPlan {
		t.Errorf("Kind = %q, want %q", a.Kind, ArtifactPlan)
	}
	if a.Type != string(ArtifactPlan) {
		t.Errorf("Type = %q, want %q", a.Type, string(ArtifactPlan))
	}
	if a.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", a.TaskID, "task-1")
	}
	if a.URI != "artifacts/plan-1.md" {
		t.Errorf("URI = %q, want %q", a.URI, "artifacts/plan-1.md")
	}
	if a.Digest == "" {
		t.Error("Digest is empty, want non-empty content hash")
	}
	if a.ID == "" {
		t.Error("ID is empty, want stable identifier")
	}
}

func TestNewArtifactDeterministicDigest(t *testing.T) {
	a1 := NewArtifact(ArtifactCodePatch, "task-2", "patches/fix.go")
	a2 := NewArtifact(ArtifactCodePatch, "task-2", "patches/fix.go")

	if a1.Digest != a2.Digest {
		t.Errorf("Digest not deterministic: %q != %q", a1.Digest, a2.Digest)
	}
	if a1.ID != a2.ID {
		t.Errorf("ID not deterministic: %q != %q", a1.ID, a2.ID)
	}
}

// TestArtifactExtendedFields verifies the spec-required fields are accessible
// and default to sensible zero values when unset.
func TestArtifactExtendedFields(t *testing.T) {
	a := Artifact{
		ID:               "a-1",
		Kind:             ArtifactPlan,
		Type:             string(ArtifactPlan),
		TaskID:           "task-9",
		CreatedBy:        "agent-7",
		CreatedAt:        time.Now(),
		Version:          2,
		Status:           "final",
		Scope:            "svc/payment",
		Provenance:       "workflow:plan",
		URI:              "artifacts/plan-9.md",
		Digest:           "abc123",
		ParentArtifactID: "a-0",
		RelatedEntities:  []string{"sym.Foo", "sym.Bar"},
	}

	if a.CreatedBy != "agent-7" {
		t.Errorf("CreatedBy = %q, want agent-7", a.CreatedBy)
	}
	if a.Version != 2 {
		t.Errorf("Version = %d, want 2", a.Version)
	}
	if a.Status != "final" {
		t.Errorf("Status = %q, want final", a.Status)
	}
	if a.Scope != "svc/payment" {
		t.Errorf("Scope = %q, want svc/payment", a.Scope)
	}
	if a.Provenance != "workflow:plan" {
		t.Errorf("Provenance = %q, want workflow:plan", a.Provenance)
	}
	if a.ParentArtifactID != "a-0" {
		t.Errorf("ParentArtifactID = %q, want a-0", a.ParentArtifactID)
	}
	if len(a.RelatedEntities) != 2 || a.RelatedEntities[0] != "sym.Foo" {
		t.Errorf("RelatedEntities = %v, want [sym.Foo sym.Bar]", a.RelatedEntities)
	}
}

// TestNewArtifactWorksWithExtendedStruct verifies NewArtifact still produces a
// valid artifact with the extended struct, leaving the new fields at zero.
func TestNewArtifactWorksWithExtendedStruct(t *testing.T) {
	a := NewArtifact(ArtifactCodePatch, "task-3", "patches/fix.go")

	if a.ID == "" || a.Kind != ArtifactCodePatch || a.TaskID != "task-3" {
		t.Errorf("NewArtifact returned unexpected core fields: %+v", a)
	}
	if a.CreatedBy != "" {
		t.Errorf("CreatedBy = %q, want zero value", a.CreatedBy)
	}
	if a.Version != 0 {
		t.Errorf("Version = %d, want 0", a.Version)
	}
	if a.Status != "" {
		t.Errorf("Status = %q, want zero value", a.Status)
	}
	if a.Scope != "" {
		t.Errorf("Scope = %q, want zero value", a.Scope)
	}
	if a.Provenance != "" {
		t.Errorf("Provenance = %q, want zero value", a.Provenance)
	}
	if a.ParentArtifactID != "" {
		t.Errorf("ParentArtifactID = %q, want zero value", a.ParentArtifactID)
	}
	if len(a.RelatedEntities) != 0 {
		t.Errorf("RelatedEntities = %v, want empty", a.RelatedEntities)
	}
}

// TestNewArtifactLinkValidKinds verifies the three P3.4 link kinds are accepted
// and the link fields are recorded correctly.
func TestNewArtifactLinkValidKinds(t *testing.T) {
	cases := []struct {
		kind ArtifactLinkKind
	}{
		{ArtifactLinkDerivedFrom},
		{ArtifactLinkSupports},
		{ArtifactLinkContradicts},
	}
	for _, c := range cases {
		link, err := NewArtifactLink("a-from", "a-to", c.kind, "reason")
		if err != nil {
			t.Fatalf("NewArtifactLink(%q) unexpected error: %v", c.kind, err)
		}
		if link.FromID != "a-from" || link.ToID != "a-to" || link.Kind != c.kind || link.Reason != "reason" {
			t.Errorf("NewArtifactLink(%q) = %+v", c.kind, link)
		}
	}
}

// TestNewArtifactLinkRejectsInvalid verifies validation: unknown kinds and
// self-links must be refused so no invalid edge enters the traceable chain.
func TestNewArtifactLinkRejectsInvalid(t *testing.T) {
	if _, err := NewArtifactLink("a", "b", ArtifactLinkKind("dangling"), ""); err == nil {
		t.Error("unknown kind: want error, got nil")
	}
	if _, err := NewArtifactLink("a", "a", ArtifactLinkSupports, ""); err == nil {
		t.Error("self-link: want error, got nil")
	}
	if _, err := NewArtifactLink("", "b", ArtifactLinkSupports, ""); err == nil {
		t.Error("empty from: want error, got nil")
	}
	if _, err := NewArtifactLink("a", "", ArtifactLinkSupports, ""); err == nil {
		t.Error("empty to: want error, got nil")
	}
}

// TestArtifactLinksRoundTrip verifies links recorded on an Artifact are
// preserved, making the traceability chain auditable.
func TestArtifactLinksRoundTrip(t *testing.T) {
	report := NewArtifact(ArtifactRiskReport, "task-1", "reports/risk-1.md")
	plan := NewArtifact(ArtifactPlan, "task-1", "plans/plan-1.md")

	link, err := NewArtifactLink(plan.ID, report.ID, ArtifactLinkDerivedFrom, "plan is derived from the risk report")
	if err != nil {
		t.Fatalf("NewArtifactLink: %v", err)
	}
	plan.Links = append(plan.Links, link)

	if len(plan.Links) != 1 {
		t.Fatalf("Links = %d, want 1", len(plan.Links))
	}
	if plan.Links[0].Kind != ArtifactLinkDerivedFrom || plan.Links[0].ToID != report.ID {
		t.Errorf("Link = %+v, want derived_from -> %s", plan.Links[0], report.ID)
	}
}
