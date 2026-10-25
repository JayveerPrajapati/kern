package mcp

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestPreloadStartsAtServe pins R6 P2 (MCP audit 2026-10-25): the background
// index preload fires at serve start, BEFORE any client request, so a fresh
// root's build is underway before the client connects — the cold-start head
// start. The fixture root is asserted via index.HasPersistedIndex and no
// request is ever sent; the stream is ended (stdin EOF) once the build lands.
func TestPreloadStartsAtServe(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// preloadIndexes skips roots that are not plausible projects (no .kern
	// index, not a git work tree) so a scratch cwd never gets a junk index;
	// make the fixture a real git work tree (git init only — no commit, so
	// no global pre-commit hook can fire).
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	t.Setenv("KERN_PRELOAD", "1")
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_MCP_WATCH", "0") // the watch tick must not be the builder

	pr, pw := io.Pipe()
	srv := NewServer(pr, pw)
	done := make(chan struct{})
	go func() {
		_ = ServeStdio(srv)
		close(done)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for !index.HasPersistedIndex(root) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// End the stream so the server exits and the goroutine is reclaimed.
	_ = pr.CloseWithError(io.EOF)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not exit after stdin EOF")
	}
	if !index.HasPersistedIndex(root) {
		t.Fatal("serve-start preload did not build the root index")
	}
}
