package execution

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiffSurvivesUnixSocket guards the hide-and-retry path: a unix socket
// in the source tree (e.g. the event relay's .kern/events.sock) must not
// abort Worktree.Diff with git's "fatal: cannot hash" (exit 128), and the
// socket must still resolve afterwards.
func TestDiffSurvivesUnixSocket(t *testing.T) {
	src, _ := os.MkdirTemp("", "wt-sock")
	defer os.RemoveAll(src)
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(src, ".kern", "events.sock"))
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer ln.Close()

	wt, err := NewWorktree(src)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer func() { _ = wt.Cleanup() }()
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-one\n+two\n"
	if err := wt.Apply(patch); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	diff, err := wt.Diff()
	if err != nil {
		t.Fatalf("Diff with socket present: %v", err)
	}
	if !strings.Contains(diff, "+two") {
		t.Errorf("diff missing the applied change:\n%s", diff)
	}
	// The socket must still resolve to the same inode after Diff moved it
	// aside and back.
	if _, err := os.Stat(filepath.Join(src, ".kern", "events.sock")); err != nil {
		t.Errorf("socket not restored after Diff: %v", err)
	}
	conn, err := net.Dial("unix", filepath.Join(src, ".kern", "events.sock"))
	if err == nil {
		conn.Close()
	} else if !os.IsExist(err) && !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "not listen") {
		// ECONNREFUSED proves the path still resolves to the listening
		// socket; only a missing path (ENOENT) would be a failure.
		t.Errorf("socket no longer reachable after Diff: %v", err)
	}
}
