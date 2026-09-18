package loop

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
//
// The real-execution opt-in env vars are set ONCE here instead of per-test
// t.Setenv: t.Setenv panics after t.Parallel(), and the heavy loop tests run
// in parallel. No loop test asserts the KERN_ALLOW_DEPLOY default-block
// ("deploy skipped: KERN_ALLOW_DEPLOY not set"), so enabling it globally is
// safe — the tests that exercise deploy all set it themselves today.
func TestMain(m *testing.M) {
	_ = os.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	_ = os.Setenv("KERN_ALLOW_DEPLOY", "1")     // production mutation opt-in (no test asserts the default block)
	if os.Getenv("XDG_CACHE_HOME") == "" {
		dir, err := os.MkdirTemp("", "kern-test-loop-*")
		if err != nil {
			panic(err)
		}
		_ = os.Setenv("XDG_CACHE_HOME", dir)
		code := m.Run()
		_ = os.RemoveAll(dir)
		os.Exit(code)
	}
	os.Exit(m.Run())
}
