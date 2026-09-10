package service

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryAddListRecallClear exercises the full memory lifecycle through
// the service: add, list, recall, clear.
func TestMemoryAddListRecallClear(t *testing.T) {
	root := t.TempDir()
	svc := New()
	ctx := context.Background()

	// List on an empty store is not an error.
	entries, err := svc.Memory.List(ctx, root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty memory, got %d entries", len(entries))
	}

	// Add two lessons.
	if err := svc.Memory.Add(ctx, root, "the service layer wraps the engines"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := svc.Memory.Add(ctx, root, "kern uses golang"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err = svc.Memory.List(ctx, root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Recall surfaces the relevant lesson.
	recalled, err := svc.Memory.Recall(ctx, root, "service layer abstraction", 5)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(recalled) == 0 {
		t.Fatal("Recall returned nothing for a known lesson")
	}
	found := false
	for _, e := range recalled {
		if strings.Contains(e.Text, "service layer") {
			found = true
		}
	}
	if !found {
		t.Errorf("recalled lessons missing the matching lesson: %+v", recalled)
	}

	// Clear empties the store.
	if err := svc.Memory.Clear(ctx, root); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	entries, err = svc.Memory.List(ctx, root)
	if err != nil {
		t.Fatalf("List after Clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty memory after Clear, got %d entries", len(entries))
	}
}
