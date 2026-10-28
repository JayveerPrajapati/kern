package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/semcache"
)

// TestSemcacheFlagsMatchMCP pins CLI/MCP argument parity: --namespace is the
// CLI spelling of the MCP namespace arg, and --a/--b mirror the MCP sim args.
func TestSemcacheFlagsMatchMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.Ensure(); err != nil {
		t.Fatal(err)
	}
	_ = semcache.Clear("")
	if err := semcache.Store("prompt", "the queue worker crashed", "p"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { runSemcache([]string{"list", "--namespace", "prompt"}) })
	if !strings.Contains(out, `semcache "prompt"`) || !strings.Contains(out, "queue worker") {
		t.Fatalf("list --namespace did not read the namespace:\n%s", out)
	}

	out = captureStdout(t, func() {
		runSemcache([]string{"sim", "--a", "alpha beta", "--b", "alpha beta"})
	})
	if !strings.Contains(out, "similarity: 1.000") {
		t.Fatalf("sim --a/--b did not compute:\n%s", out)
	}
}
