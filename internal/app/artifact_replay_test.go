package app

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestArtifactReplay verifies that Replay reconstructs the artifact chain in
// order (following ParentArtifactID links from root).
func TestArtifactReplay(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	taskID := "t-replay"
	now := time.Now().UTC()

	// Create a chain: ContextPacket → Analysis → Impact → Plan.
	artifacts := []domain.Artifact{
		{ID: "a1", TaskID: taskID, Kind: domain.ArtifactContextPacket, Status: "final", CreatedAt: now, Digest: "d1"},
		{ID: "a2", TaskID: taskID, Kind: domain.ArtifactAnalysisReport, Status: "final", CreatedAt: now.Add(1 * time.Second), ParentArtifactID: "a1", Digest: "d2"},
		{ID: "a3", TaskID: taskID, Kind: domain.ArtifactImpactReport, Status: "final", CreatedAt: now.Add(2 * time.Second), ParentArtifactID: "a2", Digest: "d3"},
		{ID: "a4", TaskID: taskID, Kind: domain.ArtifactPlan, Status: "final", CreatedAt: now.Add(3 * time.Second), ParentArtifactID: "a3", Digest: "d4"},
	}
	for _, a := range artifacts {
		if _, err := store.Save(a); err != nil {
			t.Fatalf("Save(%s): %v", a.ID, err)
		}
	}

	chain, err := store.Replay(taskID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(chain) != 4 {
		t.Fatalf("len(chain)=%d, want 4", len(chain))
	}
	// Verify chain order: a1 → a2 → a3 → a4.
	expected := []string{"a1", "a2", "a3", "a4"}
	for i, id := range expected {
		if chain[i].ID != id {
			t.Fatalf("chain[%d].ID=%s, want %s", i, chain[i].ID, id)
		}
	}
}

// TestArtifactReplayEmpty verifies Replay returns empty for a task with no
// artifacts.
func TestArtifactReplayEmpty(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	chain, err := store.Replay("nonexistent")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(chain) != 0 {
		t.Fatalf("len(chain)=%d, want 0", len(chain))
	}
}

// TestArtifactCompare verifies Compare reports which artifact kinds are present
// in each task and where digests differ.
func TestArtifactCompare(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	now := time.Now().UTC()

	// Task 1: ContextPacket + Analysis + Impact.
	for _, a := range []domain.Artifact{
		{ID: "b1", TaskID: "t1", Kind: domain.ArtifactContextPacket, Status: "final", CreatedAt: now, Digest: "x1"},
		{ID: "b2", TaskID: "t1", Kind: domain.ArtifactAnalysisReport, Status: "final", CreatedAt: now.Add(1 * time.Second), ParentArtifactID: "b1", Digest: "x2"},
		{ID: "b3", TaskID: "t1", Kind: domain.ArtifactImpactReport, Status: "final", CreatedAt: now.Add(2 * time.Second), ParentArtifactID: "b2", Digest: "x3"},
	} {
		store.Save(a)
	}

	// Task 2: ContextPacket + Analysis + Plan (Impact missing, Plan added, Analysis digest differs).
	for _, a := range []domain.Artifact{
		{ID: "c1", TaskID: "t2", Kind: domain.ArtifactContextPacket, Status: "final", CreatedAt: now, Digest: "x1"},
		{ID: "c2", TaskID: "t2", Kind: domain.ArtifactAnalysisReport, Status: "final", CreatedAt: now.Add(1 * time.Second), ParentArtifactID: "c1", Digest: "DIFFERENT"},
		{ID: "c3", TaskID: "t2", Kind: domain.ArtifactPlan, Status: "final", CreatedAt: now.Add(2 * time.Second), ParentArtifactID: "c2", Digest: "x4"},
	} {
		store.Save(a)
	}

	cmp, err := store.Compare("t1", "t2")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	// ContextPacket is in both with same digest.
	found := false
	for _, k := range cmp.InBoth {
		if k == string(domain.ArtifactContextPacket) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ContextPacket not in InBoth: %v", cmp.InBoth)
	}
	// ImpactReport is only in task 1.
	found = false
	for _, k := range cmp.OnlyIn1 {
		if k == string(domain.ArtifactImpactReport) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ImpactReport not in OnlyIn1: %v", cmp.OnlyIn1)
	}
	// Plan is only in task 2.
	found = false
	for _, k := range cmp.OnlyIn2 {
		if k == string(domain.ArtifactPlan) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Plan not in OnlyIn2: %v", cmp.OnlyIn2)
	}
	// AnalysisReport digest differs.
	dd, ok := cmp.DigestDiff[string(domain.ArtifactAnalysisReport)]
	if !ok {
		t.Fatalf("AnalysisReport not in DigestDiff: %v", cmp.DigestDiff)
	}
	if dd[0] != "x2" || dd[1] != "DIFFERENT" {
		t.Fatalf("AnalysisReport digest diff = %v, want [x2, DIFFERENT]", dd)
	}
}

// TestArtifactDraftReplaceable verifies that draft artifacts CAN be replaced
// (complements the existing TestArtifactImmutability in vertical_slice_test.go).
func TestArtifactDraftReplaceable(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	a1 := domain.Artifact{
		ID: "dr", TaskID: "t", Kind: domain.ArtifactPlan, Status: "draft",
		CreatedAt: time.Now().UTC(), Digest: "v1",
	}
	store.Save(a1)
	a2 := a1
	a2.Digest = "v2"
	if _, err := store.Save(a2); err != nil {
		t.Fatalf("Save draft: %v", err)
	}
	got, _ := store.Get("dr")
	if got.Digest != "v2" {
		t.Fatalf("digest=%s, want v2 (drafts are replaceable)", got.Digest)
	}
}
