package mcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preloadTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/p\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte("package p\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// NEW-2: the preload plausibility gate requires a real repo — .git dir
	// with a HEAD file (LOW-7 contract) — or the preload is skipped.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// Close must drain the background preload instead of abandoning it mid-build:
// when Close returns, the preload has finished and recorded its build.
func TestCloseDrainsRunningPreload(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.preloadIndexes()
	s.Close()

	recorded := false
	s.indexedRoots.Range(func(_, _ any) bool { recorded = true; return false })
	if !recorded {
		t.Fatal("Close returned before the background preload finished its build")
	}
}

// After shutdown has begun no new preload may start: it would build an index
// and open a session (watcher included) that nothing will ever close.
func TestNoPreloadAfterClose(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.Close()
	s.preloadIndexes()
	s.preloadWG.Wait()

	s.indexedRoots.Range(func(k, _ any) bool {
		t.Errorf("preload ran after Close for %v", k)
		return true
	})
}
