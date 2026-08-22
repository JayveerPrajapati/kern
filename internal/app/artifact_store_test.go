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