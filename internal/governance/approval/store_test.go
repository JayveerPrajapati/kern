package approval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestFileStoreAddPendingAndLoad(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	a := domain.Approval{
		ID:          "appr-1",
		TaskID:      "task-1",
		Requester:   "agent-a",
		Status:      "pending",
		Reason:      "high risk write",
		RequestedAt: time.Now(),
	}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 approval, got %d", len(got))
	}
	if got[0].ID != "appr-1" || got[0].TaskID != "task-1" {
		t.Fatalf("unexpected approval: %+v", got[0])
	}
}

func TestFileStoreDecideApprove(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	a := domain.Approval{ID: "appr-2", TaskID: "task-2", Status: "pending", RequestedAt: time.Now()}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Decide("appr-2", "human-1", true, "")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "approved" {
		t.Fatalf("want approved, got %q", got.Status)
	}
	if got.Approver != "human-1" {
		t.Fatalf("want approver human-1, got %q", got.Approver)
	}
	if got.DecidedAt == nil {
		t.Fatal("DecidedAt should be set")
	}

	// Persisted across reload.
	reloaded, err := s.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Status != "approved" {
		t.Fatalf("reload mismatch: %+v", reloaded)
	}
}

func TestFileStoreDecideReject(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	a := domain.Approval{ID: "appr-3", TaskID: "task-3", Status: "pending", RequestedAt: time.Now()}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Decide("appr-3", "human-2", false, "not approved")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "rejected" {
		t.Fatalf("want rejected, got %q", got.Status)
	}
	if got.Reason != "not approved" {
		t.Fatalf("want reason carried through, got %q", got.Reason)
	}
}

func TestFileStorePending(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	old := domain.Approval{ID: "appr-old", TaskID: "t1", Status: "pending", RequestedAt: time.Now().Add(-time.Hour)}
	new := domain.Approval{ID: "appr-new", TaskID: "t2", Status: "pending", RequestedAt: time.Now()}
	done := domain.Approval{ID: "appr-done", TaskID: "t3", Status: "approved", RequestedAt: time.Now()}
	for _, a := range []domain.Approval{new, done, old} {
		if err := s.AddPending(a); err != nil {
			t.Fatalf("AddPending: %v", err)
		}
	}

	pending, err := s.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("want 2 pending, got %d", len(pending))
	}
	// Sorted by RequestedAt ascending.
	if pending[0].ID != "appr-old" || pending[1].ID != "appr-new" {
		t.Fatalf("unexpected order: %+v", pending)
	}
}

func TestFileStoreGetNotFound(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	if _, err := s.Get("appr-nope"); err == nil {
		t.Fatal("want error for missing approval")
	}
}

func TestFileStoreLoadMissingFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	s := NewFileStore(root)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil slice, got %+v", got)
	}

	// Ensure the dir is created even when loading.
	if _, err := os.Stat(filepath.Join(root, ".kern")); err != nil {
		t.Fatalf("expected .kern dir to be created: %v", err)
	}
}