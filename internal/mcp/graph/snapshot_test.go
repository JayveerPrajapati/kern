package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestSnapshotCreateVerifyRoundTrip pins the kern_snapshot leaf contract:
// SnapshotCreate renders the index snapshot as indented JSON, and
// SnapshotVerify loads that file back and verifies it against the
// working tree at the resolved root — the exact behavior the mcp
// adapter delegates to.
func TestSnapshotCreateVerifyRoundTrip(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a.go")
	if err := os.WriteFile(src, []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}

	// create: whole-tree snapshot rendered as indented JSON.
	out, err := SnapshotCreate(ix, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("create output not JSON: %v", err)
	}
	if _, ok := snap["schema_version"]; !ok {
		t.Fatalf("create output missing schema_version: %v", snap)
	}

	// The caller persists the rendered snapshot; write it to disk.
	file := filepath.Join(root, "snap.json")
	if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}

	// verify: verdict JSON carrying the snapshot identity fields.
	vout, err := SnapshotVerify(root, map[string]any{"file": file})
	if err != nil {
		t.Fatal(err)
	}
	var verdict map[string]any
	if err := json.Unmarshal([]byte(vout), &verdict); err != nil {
		t.Fatalf("verify output not JSON: %v", err)
	}
	for _, key := range []string{"verdict", "content_root", "checked_files", "schema_version", "strict"} {
		if _, ok := verdict[key]; !ok {
			t.Errorf("verify output missing %q: %v", key, verdict)
		}
	}

	// verify without a file argument fails loud.
	if _, err := SnapshotVerify(root, map[string]any{}); err == nil {
		t.Error("verify without file: want error, got nil")
	}
}
