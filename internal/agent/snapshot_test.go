package agent

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestSnapshotRecordAndHistory verifies that snapshots are recorded on each
// state change and retrievable via History.
func TestSnapshotRecordAndHistory(t *testing.T) {
	store := NewSnapshotStore(t.TempDir())
	tk := NewTask("code", "x")
	_ = tk.Start("bot-1") // CREATED → ANALYZING

	if err := store.Record(*tk); err != nil {
		t.Fatalf("Record: %v", err)
	}
	_ = tk.Transition(domain.TaskPlanning)
	if err := store.Record(*tk); err != nil {
		t.Fatalf("Record: %v", err)
	}
	_ = tk.Block("waiting")
	if err := store.Record(*tk); err != nil {
		t.Fatalf("Record: %v", err)
	}

	history, err := store.History(tk.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("len(history)=%d, want 3", len(history))
	}
	// Snapshots should be chronological.
	if history[0].State != domain.TaskAnalyzing {
		t.Fatalf("history[0].State=%s, want ANALYZING", history[0].State)
	}
	if history[1].State != domain.TaskPlanning {
		t.Fatalf("history[1].State=%s, want PLANNING", history[1].State)
	}
	if history[2].State != domain.TaskBlocked {
		t.Fatalf("history[2].State=%s, want BLOCKED", history[2].State)
	}
}

// TestSnapshotListByState verifies the history index returns task IDs by their
// most recent state.
func TestSnapshotListByState(t *testing.T) {
	store := NewSnapshotStore(t.TempDir())

	tk1 := NewTask("code", "a")
	_ = tk1.Start("bot-1")
	_ = store.Record(*tk1)

	tk2 := NewTask("code", "b")
	_ = tk2.Start("bot-2")
	_ = tk2.Fail("boom")
	_ = store.Record(*tk2)

	tk3 := NewTask("code", "c")
	_ = tk3.Start("bot-3")
	_ = tk3.Cancel("nope")
	_ = store.Record(*tk3)

	// tk1 is ANALYZING, tk2 is FAILED, tk3 is CANCELLED.
	analyzing, err := store.ListByState(domain.TaskAnalyzing)
	if err != nil {
		t.Fatalf("ListByState(ANALYZING): %v", err)
	}
	if len(analyzing) != 1 || analyzing[0] != tk1.ID {
		t.Fatalf("ListByState(ANALYZING)=%v, want [%s]", analyzing, tk1.ID)
	}

	failed, err := store.ListByState(domain.TaskFailed)
	if err != nil {
		t.Fatalf("ListByState(FAILED): %v", err)
	}
	if len(failed) != 1 || failed[0] != tk2.ID {
		t.Fatalf("ListByState(FAILED)=%v, want [%s]", failed, tk2.ID)
	}
}

// TestSnapshotListSince verifies the history index returns task IDs that
// changed since a given time.
func TestSnapshotListSince(t *testing.T) {
	store := NewSnapshotStore(t.TempDir())

	tk1 := NewTask("code", "a")
	_ = tk1.Start("bot-1")
	_ = store.Record(*tk1)

	before := time.Now().Add(-1 * time.Millisecond)

	tk2 := NewTask("code", "b")
	_ = tk2.Start("bot-2")
	_ = store.Record(*tk2)

	// tk2 changed after `before`, tk1 did not.
	since, err := store.ListSince(before)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	// Both tasks might be returned if timestamps are within the same
	// millisecond; verify at least tk2 is present.
	found := false
	for _, id := range since {
		if id == tk2.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListSince did not include %s: %v", tk2.ID, since)
	}
}

// TestSnapshotPersistAcrossInstances verifies snapshots survive a "restart"
// (new SnapshotStore at the same path).
func TestSnapshotPersistAcrossInstances(t *testing.T) {
	store := NewSnapshotStore(t.TempDir())
	tk := NewTask("code", "x")
	_ = tk.Start("bot-1")
	_ = store.Record(*tk)

	store2 := NewSnapshotStore(store.root)
	history, err := store2.History(tk.ID)
	if err != nil {
		t.Fatalf("History after restart: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history)=%d after restart, want 1", len(history))
	}
	if history[0].State != domain.TaskAnalyzing {
		t.Fatalf("history[0].State=%s, want ANALYZING", history[0].State)
	}
}
