package mcp

import (
	"os"
	"testing"
)

// TestMain isolates this package's test binary from the real kern cache and
// from other packages' parallel test binaries. JSON stores (tasks, snapshots,
// artifacts) are keyed by project root; without isolation, every test run
// appends to the same per-root files in the user's ~/.cache/kern and parallel
// test binaries race on the shared file (each process's load->modify->save
// interleaves and loses updates). Redirecting XDG_CACHE_HOME to a throwaway
// dir gives this binary a private, empty store — faster and deterministic.
// The redirect is skipped when XDG_CACHE_HOME is already set: child test
// processes (e.g. the cross-process store tests) inherit the parent's cache
// dir and must keep writing to the SAME store the parent reads.
func TestMain(m *testing.M) {
	// G-2: keep the tool-call audit chain out of the package dir's real
	// .kern/audit store while tests run (individual tests may override with
	// t.Setenv for their own assertions).
	auditDir, err := os.MkdirTemp("", "kern-test-mcp-audit-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("KERN_MCP_AUDIT_DIR", auditDir)
	if os.Getenv("XDG_CACHE_HOME") == "" {
		dir, err := os.MkdirTemp("", "kern-test-mcp-*")
		if err != nil {
			panic(err)
		}
		_ = os.Setenv("XDG_CACHE_HOME", dir)
		code := m.Run()
		_ = os.RemoveAll(dir)
		_ = os.RemoveAll(auditDir)
		os.Exit(code)
	}
	code := m.Run()
	_ = os.RemoveAll(auditDir)
	os.Exit(code)
}
