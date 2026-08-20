package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestRestoreKeepsFilesOverSizeCap: files too big to snapshot must survive a
// rollback untouched — previously the size-cap fs.SkipDir left them out of the
// snapshot and Restore deleted them as "new" (silent data loss).
func TestRestoreKeepsFilesOverSizeCap(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	_ = os.WriteFile(filepath.Join(root, "small.txt"), []byte("original"), 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if len(snap.files) != 1 || snap.files[0] != "small.txt" {
		t.Fatalf("expected only small.txt snapshotted, got %v", snap.files)
	}
	// A failed run must leave big.dat alone: it pre-dates the run and was
	// never copied into the snapshot, so deleting it would lose data.
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "big.dat")); err != nil || fi.Size() != int64(len(big)) {
		t.Fatalf("big.dat deleted or truncated by restore: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "small.txt"))
	if string(b) != "original" {
		t.Fatalf("small.txt not restored: %q", b)
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
	if Escape("/proj", "sub/../ok.txt") {
		t.Fatal("cleaned in-root path should not be flagged as escape")
	}
	if !Escape("/proj", "sub/../../evil.txt") {
		t.Fatal("expected traversal past root to be detected")
	}
}

// TestEscapeRelativeRoot is a regression test for a critical data-loss bug:
// when root is a relative path like "." (the runSandbox default), the old
// Escape computed filepath.Clean(".")+"./" as the prefix, which never matched
// joined paths like "go.mod". Every file appeared to "escape", so Restore
// refused to copy any file back, then the cleanup pass deleted the entire
// working tree. Escape must normalize root to an absolute path first.
func TestEscapeRelativeRoot(t *testing.T) {
	// "." — the runSandbox default that caused the data loss.
	if Escape(".", "go.mod") {
		t.Fatal(`relative root "." flagged in-root file as escape — data-loss regression`)
	}
	if Escape(".", "cmd/kern/main.go") {
		t.Fatal(`relative root "." flagged nested in-root file as escape`)
	}
	if !Escape(".", "../etc/passwd") {
		t.Fatal(`relative root "." failed to detect genuine traversal`)
	}
	// A relative subdirectory root must also work.
	if Escape("sub", "file.txt") {
		t.Fatal(`relative subdirectory root flagged in-root file as escape`)
	}
	if !Escape("sub", "../escape.txt") {
		t.Fatal(`relative subdirectory root failed to detect traversal`)
	}
}

// TestRestoreDetectsDeletedSkipFile: a file that was skipped at snapshot time
// (over the size cap) and then DELETED by the failed run cannot be restored, but
// Restore must report the loss loudly rather than silently.
func TestRestoreDetectsDeletedSkipFile(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if _, ok := snap.skipped["big.dat"]; !ok {
		t.Fatal("expected big.dat to be marked skipped at snapshot time")
	}
	// The failed run deletes the over-cap file. It cannot be restored from the
	// snapshot (no copy is held), so Restore must fail loudly about the loss.
	if err := os.Remove(filepath.Join(root, "big.dat")); err != nil {
		t.Fatal(err)
	}
	err = snap.Restore()
	if err == nil {
		t.Fatal("Restore returned nil after a skipped file was deleted by the run; want a loud error")
	}
	if !strings.Contains(err.Error(), "big.dat") {
		t.Fatalf("restore error should name the lost file: %v", err)
	}
	if !strings.Contains(err.Error(), "DELETED") {
		t.Fatalf("restore error should flag the deletion loudly: %v", err)
	}
}

// TestRestoreNoErrorWhenSkipFileUntouched: a skipped file that survived the run
// must not trigger the loud-loss error on restore.
func TestRestoreNoErrorWhenSkipFileUntouched(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore errored even though the skip file was untouched: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, "big.dat")); err != nil || fi.Size() != int64(len(big)) {
		t.Fatalf("big.dat missing or changed after restore: %v", err)
	}
}

// TestRestoreRefusesTraversalPath ensures Restore fails closed when a snapshot
// file's path would escape the root, using the Escape() defense.
func TestRestoreRefusesTraversalPath(t *testing.T) {
	root := t.TempDir()
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	// Hand-craft a snapshot file entry that attempts a traversal.
	snap.files = append(snap.files, filepath.Join("..", "escape.txt"))
	if err := snap.Restore(); err == nil {
		t.Fatal("Restore allowed a traversal path; want a loud error")
	}
	if _, statErr := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatal("traversal file should not have been written outside the root")
	}
}
