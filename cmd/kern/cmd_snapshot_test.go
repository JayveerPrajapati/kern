package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotFixture writes a tiny Go module for snapshot round-trip tests.
func snapshotFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module snapfixture\n\ngo 1.20\n",
		"main.go":      "package main\n\nfunc helper() string { return \"h\" }\n\nfunc main() { _ = helper() }\n",
		"util/util.go": "package util\n\nfunc U() int { return 1 }\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestSnapshotVerifyRoundtrip (F-008): a snapshot written with
// `kern snapshot --out snap.json` must verify cleanly against the same repo
// with `kern snapshot --verify snap.json` (previously the positional path was
// treated as a repo root, producing an empty fresh snapshot).
func TestSnapshotVerifyRoundtrip(t *testing.T) {
	dir := snapshotFixture(t)
	outPath := filepath.Join(t.TempDir(), "snap.json")

	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runSnapshot([]string{dir, "--out", outPath})
	})
	if code != 0 {
		t.Fatalf("snapshot create exit code = %d, want 0 (stderr above)", code)
	}
	if !strings.Contains(out, "symbols=") {
		t.Fatalf("snapshot create output missing summary: %q", out)
	}

	// --verify reads the file back and compares against the same repo.
	out = captureStdout(t, func() {
		code = runSnapshot([]string{"--verify", outPath, "--root", dir})
	})
	if code != 0 {
		t.Fatalf("snapshot verify exit code = %d, want 0 (stderr above)", code)
	}
	if !strings.Contains(out, "verdict: fresh") {
		t.Fatalf("snapshot verify should report fresh, got: %q", out)
	}
	if !strings.Contains(out, "tree_oid=") {
		t.Fatalf("snapshot verify should report the tree fingerprint, got: %q", out)
	}
}

// TestSnapshotVerifyStale (F-008): touching a source file after the snapshot
// is taken must flip the verdict to stale.
func TestSnapshotVerifyStale(t *testing.T) {
	dir := snapshotFixture(t)
	outPath := filepath.Join(t.TempDir(), "snap.json")

	code := -1
	captureStdout(t, func() {
		code = runSnapshot([]string{dir, "--out", outPath})
	})
	if code != 0 {
		t.Fatalf("snapshot create exit code = %d, want 0", code)
	}

	// Modify a tracked file (go.mod: bump a line) so the git tree OID changes.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc helper() string { return \"h2\" }\n\nfunc main() { _ = helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		code = runSnapshot([]string{"--verify", outPath, "--root", dir})
	})
	if code != 1 {
		t.Fatalf("stale verify exit code = %d, want 1 (output: %q)", code, out)
	}
	if !strings.Contains(out, "verdict: stale") {
		t.Fatalf("snapshot verify should report stale after edit, got: %q", out)
	}
}

// TestSnapshotVerifyUnknown (F-008): verifying a bogus snapshot file fails
// loudly (schema mismatch / not a snapshot) with exit code 2.
func TestSnapshotVerifyUnknown(t *testing.T) {
	dir := snapshotFixture(t)
	bogus := filepath.Join(t.TempDir(), "bogus.json")
	if err := os.WriteFile(bogus, []byte(`{"not":"a snapshot"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code := runSnapshotExit(t, []string{"--verify", bogus, "--root", dir})
	if code != 2 {
		t.Fatalf("bogus snapshot verify exit code = %d, want 2 (usage/runtime error)", code)
	}
}

// TestSnapshotPositionalFileGuard (F-008): passing a snapshot FILE as the
// positional root must fail with a clear usage error instead of silently
// producing an empty fresh snapshot.
func TestSnapshotPositionalFileGuard(t *testing.T) {
	dir := snapshotFixture(t)
	outPath := filepath.Join(t.TempDir(), "snap.json")
	code := -1
	captureStdout(t, func() {
		code = runSnapshot([]string{dir, "--out", outPath})
	})
	if code != 0 {
		t.Fatalf("snapshot create exit code = %d, want 0", code)
	}
	code = runSnapshotExit(t, []string{outPath})
	if code != 2 {
		t.Fatalf("positional-file snapshot exit code = %d, want 2", code)
	}
}

// runSnapshotExit runs runSnapshot and recovers the fatalUsage/exitError
// sentinel so exit codes can be asserted in-process.
func runSnapshotExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	return runSnapshot(rest)
}
