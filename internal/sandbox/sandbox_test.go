package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotRestore(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "keep.txt"), []byte("original"), 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	// Mutate and add files.
	_ = os.WriteFile(filepath.Join(root, "keep.txt"), []byte("changed"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("extra"), 0o644)
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "keep.txt"))
	if string(b) != "original" {
		t.Fatalf("keep.txt not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new.txt should be removed")
	}
}

func TestRunRestoresOnFailure(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "data.txt"), []byte("pristine"), 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "echo changed > data.txt; exit 1"}, 10*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
	if !res.Restored {
		t.Fatal("expected restore")
	}
	b, _ := os.ReadFile(filepath.Join(root, "data.txt"))
	if string(b) != "pristine" {
		t.Fatalf("data.txt not restored: %q", b)
	}
}

func TestRunKeepsOnSuccess(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "data.txt"), []byte("pristine"), 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "echo changed > data.txt; exit 0"}, 10*time.Second)
	if !res.OK {
		t.Fatalf("expected success: %+v", res)
	}
	if res.Restored {
		t.Fatal("success should not restore")
	}
	b, _ := os.ReadFile(filepath.Join(root, "data.txt"))
	if string(b) != "changed\n" {
		t.Fatalf("expected change kept: %q", b)
	}
}

func TestRunMissingCommand(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), "definitely-not-a-real-binary-xyz", nil, 10*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
}

func TestEscape(t *testing.T) {
	if !Escape("/proj", "../evil.txt") {
		t.Fatal("expected escape detection")
	}
	if Escape("/proj", "sub/ok.txt") {
		t.Fatal("ok path flagged as escape")
	}
}
