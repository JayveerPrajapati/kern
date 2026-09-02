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

// TestSnapshotSkipsLargeFiles: a file over the snapshot cap is not read into
// the snapshot; it is recorded in skippedOverCap (and skipped), and other
// files in the same directory are still snapshotted.
func TestSnapshotSkipsLargeFiles(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	_ = os.WriteFile(filepath.Join(root, "small.txt"), []byte("ok"), 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if len(snap.skippedOverCap) != 1 || snap.skippedOverCap[0] != "big.dat" {
		t.Fatalf("skippedOverCap = %v; want [big.dat]", snap.skippedOverCap)
	}
	if len(snap.files) != 1 || snap.files[0] != "small.txt" {
		t.Fatalf("expected only small.txt snapshotted, got %v", snap.files)
	}
	// The snapshot must not contain the over-cap file's contents.
	if _, err := os.Stat(filepath.Join(snap.Tmp(), "big.dat")); !os.IsNotExist(err) {
		t.Fatal("snapshot must not contain the over-cap file")
	}
}

// TestRunWarnsOnModifiedSkippedFile: when a failed run MODIFIES a file that
// was skipped at snapshot time (over the cap), Run must refuse to present a
// rollback as complete: it returns an error naming the file and the cap,
// BEFORE attempting rollback, and the change is left in place for the caller
// to decide on.
func TestRunWarnsOnModifiedSkippedFile(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "echo x >> big.dat; exit 1"}, 10*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.Restored {
		t.Fatal("rollback must not be attempted (and claimed) when a skipped over-cap file was modified")
	}
	if res.Err == nil {
		t.Fatal("Run returned nil error after a skipped file was modified; want a clear error before rollback")
	}
	if !strings.Contains(res.Err.Error(), "big.dat") {
		t.Fatalf("error should name the skipped file: %v", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "snapshot cap") {
		t.Fatalf("error should mention the snapshot cap: %v", res.Err)
	}
	if len(res.SkippedFiles) != 1 || res.SkippedFiles[0] != "big.dat" {
		t.Fatalf("SkippedFiles = %v; want [big.dat]", res.SkippedFiles)
	}
	// The file's post-run state is left in place (nothing to restore from).
	if fi, err := os.Stat(filepath.Join(root, "big.dat")); err != nil || fi.Size() != int64(len(big))+2 {
		t.Fatalf("big.dat should retain the run's modification (size %d): %v", int64(len(big))+2, err)
	}
}

// TestMaxSnapshotBytesConfigurable: raising the cap via
// KERN_SANDBOX_MAX_SNAPSHOT_BYTES (suffixed or plain-byte form) lets files
// over the default 100 MiB cap be snapshotted normally.
func TestMaxSnapshotBytesConfigurable(t *testing.T) {
	// Suffixed form.
	t.Setenv("KERN_SANDBOX_MAX_SNAPSHOT_BYTES", "256MB")
	if got := snapshotCap(); got != 256<<20 {
		t.Fatalf("snapshotCap() with 256MB = %d; want %d", got, 256<<20)
	}
	// Plain-byte form.
	t.Setenv("KERN_SANDBOX_MAX_SNAPSHOT_BYTES", "200000000")
	if got := snapshotCap(); got != 200000000 {
		t.Fatalf("snapshotCap() with 200000000 = %d; want 200000000", got)
	}
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1) // over the default cap, under the raised one
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if len(snap.skippedOverCap) != 0 {
		t.Fatalf("file over the default cap should be snapshotted under a raised cap; skipped=%v", snap.skippedOverCap)
	}
	if len(snap.files) != 1 || snap.files[0] != "big.dat" {
		t.Fatalf("expected big.dat snapshotted, got %v", snap.files)
	}
}

// TestRollbackRestoresSkippedFileFailsClearly: restoring after a skipped
// (over-cap) file was DELETED by the run fails loudly, naming the file and
// the snapshot cap so the data loss is never silent.
func TestRollbackRestoresSkippedFileFailsClearly(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxSnapshotBytes+1)
	_ = os.WriteFile(filepath.Join(root, "big.dat"), big, 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if err := os.Remove(filepath.Join(root, "big.dat")); err != nil {
		t.Fatal(err)
	}
	err = snap.Restore()
	if err == nil {
		t.Fatal("Restore must fail loudly when a skipped file was deleted by the run")
	}
	msg := err.Error()
	if !strings.Contains(msg, "big.dat") {
		t.Fatalf("error should name the lost file: %v", err)
	}
	if !strings.Contains(msg, "snapshot cap") {
		t.Fatalf("error should name the snapshot cap: %v", err)
	}
}

func TestRunManifestCreatedModified(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "echo x > b.go && echo y >> a.go"}, 10*time.Second)
	if !res.OK {
		t.Fatalf("expected success: %+v", res)
	}
	byPath := map[string]Change{}
	for _, c := range res.Manifest {
		byPath[c.Path] = c
	}
	created, ok := byPath["b.go"]
	if !ok || created.Kind != "created" {
		t.Fatalf("expected created b.go, got %+v", res.Manifest)
	}
	if len(created.Hash) != 64 {
		t.Fatalf("expected 64-hex sha256, got %q", created.Hash)
	}
	if created.Size <= 0 {
		t.Fatalf("expected positive size, got %d", created.Size)
	}
	modified, ok := byPath["a.go"]
	if !ok || modified.Kind != "modified" {
		t.Fatalf("expected modified a.go, got %+v", res.Manifest)
	}
	if len(modified.Hash) != 64 {
		t.Fatalf("expected 64-hex sha256, got %q", modified.Hash)
	}
	if modified.Size <= 0 {
		t.Fatalf("expected positive size, got %d", modified.Size)
	}
	for _, c := range res.Manifest {
		if c.Kind == "deleted" {
			t.Fatalf("unexpected deleted entry: %+v", c)
		}
	}
}

func TestRunManifestDeleted(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "rm a.go"}, 10*time.Second)
	if !res.OK {
		t.Fatalf("expected success: %+v", res)
	}
	found := false
	for _, c := range res.Manifest {
		if c.Path == "a.go" && c.Kind == "deleted" {
			found = true
			if len(c.Hash) != 64 {
				t.Fatalf("expected 64-hex sha256 from snapshot copy, got %q", c.Hash)
			}
			if c.Size <= 0 {
				t.Fatalf("expected positive size, got %d", c.Size)
			}
		}
	}
	if !found {
		t.Fatalf("expected deleted a.go, got %+v", res.Manifest)
	}
}

func TestRunManifestOnFailureRollbackAudit(t *testing.T) {
	root := t.TempDir()
	res := Run(context.Background(), root, "sh", []string{"-c", "echo x > c.go; exit 1"}, 10*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
	if !res.Restored {
		t.Fatal("expected restore")
	}
	found := false
	for _, c := range res.Manifest {
		if c.Path == "c.go" && c.Kind == "created" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected created c.go recorded despite rollback, got %+v", res.Manifest)
	}
}

func TestRunManifestEmptyWhenNoChanges(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	res := Run(context.Background(), root, "sh", []string{"-c", "true"}, 10*time.Second)
	if !res.OK {
		t.Fatalf("expected success: %+v", res)
	}
	if len(res.Manifest) != 0 {
		t.Fatalf("expected empty manifest, got %+v", res.Manifest)
	}
}

func TestManifestSortedDeterministic(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "d.txt"), []byte("gone\n"), 0o644)
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	// Create, modify, and delete one file each, then diff twice.
	_ = os.WriteFile(filepath.Join(root, "b.go"), []byte("x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nchanged\n"), 0o644)
	if err := os.Remove(filepath.Join(root, "d.txt")); err != nil {
		t.Fatal(err)
	}
	man1, _ := snap.Manifest()
	man2, _ := snap.Manifest()
	if len(man1) != 3 {
		t.Fatalf("expected 3 changes, got %+v", man1)
	}
	if len(man1) != len(man2) {
		t.Fatalf("non-deterministic manifest: %+v vs %+v", man1, man2)
	}
	for i := range man1 {
		if man1[i] != man2[i] {
			t.Fatalf("non-deterministic manifest: %+v vs %+v", man1, man2)
		}
	}
	for i := 1; i < len(man1); i++ {
		if man1[i-1].Path >= man1[i].Path {
			t.Fatalf("manifest not sorted by Path: %+v", man1)
		}
	}
}
