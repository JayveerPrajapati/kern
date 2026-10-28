package govaudit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestArchiveAuditStore pins bounded audit retention: the active chain and its
// per-key shadow files move into an archive directory, the lock stays, and old
// archives beyond --keep are pruned.
func TestArchiveAuditStore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chain.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "entry-1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "archive-20200101-000000.000000000"), 0o700); err != nil {
		t.Fatal(err)
	}

	dest, err := ArchiveAuditStore(root, 1)
	if err != nil {
		t.Fatalf("ArchiveAuditStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "chain.jsonl")); err != nil {
		t.Fatalf("chain.jsonl not archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chain.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("active chain.jsonl should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "entry-1.json")); err != nil {
		t.Fatalf("per-key file not archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive-20200101-000000.000000000")); !os.IsNotExist(err) {
		t.Fatalf("oldest archive should be pruned with keep=1, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); err != nil {
		t.Fatalf("lock file must stay in place: %v", err)
	}
}
