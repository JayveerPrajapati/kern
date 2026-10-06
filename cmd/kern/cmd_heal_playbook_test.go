package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// TestHealPlaybookStoreLookupDriftIsMiss is the CLI twin of the MCP adapter
// drift test (internal/mcp/exec/heal_playbook_test.go): the recorded OLD
// content gates the replay — identical current content is a hit, drift is a
// MISS that escalates to the full LLM loop (deep-dive C4 poison-scope
// limiter).
func TestHealPlaybookStoreLookupDriftIsMiss(t *testing.T) {
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
	pb := &healPlaybookStore{root: root, store: store}

	if reps, ok := pb.Lookup("sig12345"); !ok || len(reps) != 1 {
		t.Fatalf("identical content must be a hit, ok=%v reps=%v", ok, reps)
	}

	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\n// drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reps, ok := pb.Lookup("sig12345"); ok {
		t.Fatalf("drifted content must be a miss, got reps=%v", reps)
	}
}
