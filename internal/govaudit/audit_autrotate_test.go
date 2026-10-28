package govaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestAutoRotateOnThreshold pins the 2026-10-27 retention policy: an append
// that crosses the size/entry threshold archives the active chain, resets the
// in-memory head, and the next append starts a fresh GENESIS chain that
// verifies. The entry-count leg is covered by countAuditChainEntries' unit
// test below; this drives the size leg end to end.
func TestAutoRotateOnThreshold(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern", "audit")
	lock := filepath.Join(dir, ".lock")
	newLog := func() *AuditLog {
		return NewAuditLog().WithStore(storage.NewLog(dir)).WithLockPath(lock)
	}
	l := newLog()
	l.Record(entry("", "before-rotate"))

	t.Setenv("KERN_AUDIT_ROTATE_BYTES", "1")
	t.Setenv("KERN_AUDIT_ROTATE_KEEP", "2")
	l.Record(entry("", "rotate-now"))

	archives := 0
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "archive-") {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("archives = %d, want 1 after crossing the byte threshold", archives)
	}

	// Disable the byte leg for the post-rotation append so the fresh chain is
	// observable instead of being archived again immediately.
	t.Setenv("KERN_AUDIT_ROTATE_BYTES", "0")
	l.Record(entry("", "after-rotate"))

	fresh := newLog()
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay after auto-rotate: %v", err)
	}
	if brk, verified := fresh.VerifyChainReport(); brk >= 0 {
		t.Fatalf("post-auto-rotate chain broken at entry %d (verified %d) — the writer chained to the archived tail", brk+1, verified)
	}
	if n := len(fresh.All()); n != 1 {
		t.Fatalf("post-rotation chain has %d entries, want exactly the one appended after rotation", n)
	}
}
