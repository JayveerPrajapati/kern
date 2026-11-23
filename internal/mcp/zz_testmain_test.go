package mcp

import (
	"os"
	"path/filepath"
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
	auditDir, err := os.MkdirTemp("", "kern-test-mcp-audit-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("KERN_MCP_AUDIT_DIR", auditDir)
	_ = os.Setenv("KERN_TOOL_CACHE_KEY", filepath.Join(auditDir, "tool-cache.key"))
	_ = os.Setenv("KERN_PRELOAD", "0")
	// This suite exercises every handler family directly through tools/call,
	// including tools that are not on the default advertised surface (the
	// advertised==invocable gate from audit F3, 2026-11-21, would otherwise
	// answer "unknown tool"). The gate itself has dedicated tests that flip
	// this escape off; the surface tests (server_filter_test.go) clear it
	// via clearMCPSurfaceEnv.
	_ = os.Setenv("KERN_MCP_ALLOW_UNADVERTISED", "1")
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
