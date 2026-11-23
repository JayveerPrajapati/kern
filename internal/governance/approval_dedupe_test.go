package governance

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// The guard re-mints on every blocked retry; a second mint for the SAME
// command must reuse the live pending request instead of adding another row
// (2026-11-10 campaign: 453 identical HIGH pending approvals).
func TestMintGateApprovalReusesPendingForSameCommand(t *testing.T) {
	root := t.TempDir()
	first, err := MintGateApproval("rm -rf ./build", root)
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	second, err := MintGateApproval("rm -rf ./build", root)
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if first != second {
		t.Fatalf("repeat mint created a new approval: %s != %s", first, second)
	}
	// A DIFFERENT command still mints its own request.
	other, err := MintGateApproval("mv ./a /tmp/a", root)
	if err != nil {
		t.Fatalf("other mint: %v", err)
	}
	if other == first {
		t.Fatal("distinct commands collapsed into one approval")
	}
	pending, err := NewFileStore(root).Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2 (dedupe by command key)", len(pending))
	}
}

// Prune removes stale PENDING rows only; decided records are history.
func TestPruneOlderThanKeepsDecided(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, a := range []domain.Approval{
		{ID: "appr-old-pending", Status: "pending", RequestedAt: old},
		{ID: "appr-old-approved", Status: "approved", RequestedAt: old},
		{ID: "appr-new-pending", Status: "pending", RequestedAt: time.Now()},
	} {
		if err := store.AddPending(a); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.PruneOlderThan(time.Now().Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1 (only the stale pending)", n)
	}
	left, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("remaining = %d, want 2 (decided record survives)", len(left))
	}
	for _, a := range left {
		if a.ID == "appr-old-pending" {
			t.Fatal("stale pending record survived the prune")
		}
	}
}
