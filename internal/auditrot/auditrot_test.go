package auditrot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := os.WriteFile(p, []byte("{\"a\":1}\n\n{\"b\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CountEntries(p); got != 2 {
		t.Fatalf("CountEntries = %d, want 2 (blank lines ignored)", got)
	}
}

func TestPolicyRejectsCustomLayout(t *testing.T) {
	if _, _, ok := Policy(t.TempDir()); ok {
		t.Fatal("Policy accepted a non <root>/.kern/audit directory")
	}
}

func TestShouldRotateBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "chain.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_AUDIT_ROTATE_BYTES", "1")
	if _, ok := ShouldRotate(p); !ok {
		t.Fatal("ShouldRotate = false with a 1-byte threshold and a 3-byte chain")
	}
	t.Setenv("KERN_AUDIT_ROTATE_BYTES", "0")
	t.Setenv("KERN_AUDIT_ROTATE_ENTRIES", "0")
	if _, ok := ShouldRotate(p); ok {
		t.Fatal("ShouldRotate = true with both thresholds disabled")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"archive-20260101-000000.000000000", "archive-20260102-000000.000000000", "keep.txt"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	Prune(dir, 1)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var archives []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "archive-") {
			archives = append(archives, e.Name())
		}
	}
	if len(archives) != 1 || archives[0] != "archive-20260102-000000.000000000" {
		t.Fatalf("archives after Prune(1) = %v, want only the newest", archives)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("non-archive entry removed: %v", err)
	}
}
