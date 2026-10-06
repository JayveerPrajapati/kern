package exec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// TestHealPlaybookLookupDriftIsMiss pins the adapter side of the poison-scope
// limiter (deep-dive C4, 2026-10-03): the recorded OLD content gates the
// replay. A lookup against identical current content is a hit; any drift (the
// file legitimately changed since recording) reports a MISS so the heal loop
// escalates to the full LLM round instead of overwriting current content with
// the recorded replacement.
func TestHealPlaybookLookupDriftIsMiss(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := heal.EncodeReplacements(root, []heal.Replacement{
		{Path: "app.go", Content: "package main\n\nfunc main() {}\n"},
	})
	store := incident.NewPlaybookStore(root)
	if store == nil {
		t.Fatal("NewPlaybookStore returned nil")
	}
	if err := store.UpsertBySignature("sig12345", steps); err != nil {
		t.Fatal(err)
	}
	pb := &mcpHealPlaybookStore{root: root, store: store}

	if reps, ok := pb.Lookup("sig12345"); !ok || len(reps) != 1 {
		t.Fatalf("identical content must be a hit, ok=%v reps=%v", ok, reps)
	}

	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\n// drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reps, ok := pb.Lookup("sig12345"); ok {
		t.Fatalf("drifted content must be a miss, got reps=%v", reps)
	}

	if _, ok := pb.Lookup("unknown-sig"); ok {
		t.Fatal("unknown signature must be a miss")
	}
}
