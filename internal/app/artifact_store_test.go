package app

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestFinalArtifactImmutableAcrossStatusChange verifies that a finalized
// artifact (Status == "final") cannot be overwritten even when re-saved with a
// non-final Status. Invariant 8 requires finalized artifacts to be immutable
// regardless of the new artifact's Status.
func TestFinalArtifactImmutableAcrossStatusChange(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	final := domain.Artifact{
		ID: "a-immutable", TaskID: "t", Kind: domain.ArtifactPlan, Status: "final",
		CreatedAt: time.Now().UTC(), Digest: "v1",
	}
	if _, err := store.Save(final); err != nil {
		t.Fatalf("Save(final): %v", err)
	}

	// Attempt to overwrite the final artifact with the same ID but a draft
	// status — this must be rejected.
	draft := final
	draft.Status = "draft"
	draft.Digest = "v2"
	if _, err := store.Save(draft); err == nil {
		t.Fatal("Save(draft over final) = nil error, want immutable error")
	}

	// The original finalized artifact must be unchanged.
	got, err := store.Get("a-immutable")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "final" {
		t.Errorf("Status=%s, want final (must remain unchanged)", got.Status)
	}
	if got.Digest != "v1" {
		t.Errorf("Digest=%s, want v1 (must remain unchanged)", got.Digest)
	}
}

// TestNewVersionSupersedesFinal verifies the Phase 3 "new version instead"
// rule: a finalized artifact is never silently mutated; NewVersion marks the
// original superseded (kept intact for audit) and writes a successor with
// Version+1 linked via ParentArtifactID.
func TestNewVersionSupersedesFinal(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	orig := domain.Artifact{
		ID: "a-ver", TaskID: "t", Kind: domain.ArtifactPlan, Status: "final",
		CreatedAt: time.Now().UTC(), Digest: "v1",
	}
	if _, err := store.Save(orig); err != nil {
		t.Fatalf("Save(orig): %v", err)
	}

	// Produce a new version of the same artifact (same ID, new digest/scope).
	next := orig
	next.Digest = "v2"
	next.Scope = "revised"
	saved, err := store.NewVersion(next)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	if saved.Version != 1 {
		t.Errorf("Version = %d, want 1", saved.Version)
	}
	if saved.ParentArtifactID != "a-ver" {
		t.Errorf("ParentArtifactID = %q, want a-ver", saved.ParentArtifactID)
	}
	if saved.Status != "final" {
		t.Errorf("Status = %q, want final on the new version", saved.Status)
	}

	// The original must remain readable, unchanged, marked superseded.
	origGot, err := store.Get("a-ver")
	if err != nil {
		t.Fatalf("Get(original): %v", err)
	}
	if origGot.Status != "superseded" {
		t.Errorf("original Status = %q, want superseded", origGot.Status)
	}
	if origGot.Digest != "v1" {
		t.Errorf("original Digest = %q, want v1 (unchanged)", origGot.Digest)
	}

	// The chain (GetByTask) must contain both records, original first.
	chain, err := store.GetByTask("t")
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain len = %d, want 2 (superseded + new version)", len(chain))
	}
	if chain[0].Status != "superseded" {
		t.Errorf("chain[0] Status = %q, want superseded (deterministic order)", chain[0].Status)
	}
	if chain[1].Version != 1 {
		t.Errorf("chain[1] Version = %d, want 1", chain[1].Version)
	}
}

// TestNewVersionNoExistingFallsBackToSave verifies that NewVersion with an
// unknown ID simply persists the artifact as an initial version.
func TestNewVersionNoExistingFallsBackToSave(t *testing.T) {
	store := NewArtifactStore(t.TempDir())
	a := domain.Artifact{ID: "brand-new", Kind: domain.ArtifactDiff, Status: "final", Digest: "v1"}
	saved, err := store.NewVersion(a)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	if saved.Version != 0 {
		t.Errorf("Version = %d, want 0 (initial)", saved.Version)
	}
	if saved.ID != "brand-new" {
		t.Errorf("ID = %q, want brand-new", saved.ID)
	}
}