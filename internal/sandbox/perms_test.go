package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRestorePreservesFileMode: a rollback must restore the pre-run permission
// bits alongside the content. writeAtomic writes via a 0600 temp file +
// rename, so without the recorded mode every restored file would come back
// 0600 — a failed run silently stripped executability from every script in
// the tree (F25).
func TestRestorePreservesFileMode(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "data.txt")
	if err := os.WriteFile(plain, []byte("pristine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	// Simulate a failed run's side effects: content and mode both changed.
	if err := os.WriteFile(script, []byte("hacked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("touched\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("script content not restored: %q", b)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("script mode not restored: got %o, want 755", fi.Mode().Perm())
	}
	b, err = os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "pristine\n" {
		t.Fatalf("plain content not restored: %q", b)
	}
	fi, err = os.Stat(plain)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("plain mode not restored: got %o, want 644", fi.Mode().Perm())
	}
}

// TestRunRestoresScriptModeOnFailure is the end-to-end form of the finding:
// a sandboxed command that overwrites a 0755 script (and chmods it) then
// fails must roll back both content and mode.
func TestRunRestoresScriptModeOnFailure(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	root := t.TempDir()
	script := filepath.Join(root, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := Run(context.Background(), root, "sh", []string{"-c", "echo hacked > run.sh; chmod 600 run.sh; exit 1"}, 10*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
	if !res.Restored {
		t.Fatal("expected restore")
	}
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("script content not restored: %q", b)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("script mode not restored: got %o, want 755", fi.Mode().Perm())
	}
}
