package verification

import (
	"os"
	"testing"
)

// TestMain opts this package's test binary into unisolated real executions
// (sandbox go build / go test / security / architecture / dependency checks)
// ONCE here instead of per-test t.Setenv: t.Setenv panics after t.Parallel(),
// and the heavy real-execution tests in engine_test.go run in parallel.
//
// No shared-cache redirect is needed: every fixture root is a distinct
// t.TempDir(), the intel index lives under <root>/.kern, and the sandbox
// snapshots under the OS temp dir — so parallel tests never collide on files.
// Tests that depend on the env being effectively unset
// (TestVerifyTestsIsolationRefusalIsSkipped) clear it locally with
// t.Setenv("KERN_ALLOW_UNISOLATED", "") and stay non-parallel, so the
// manipulation is race-free by design (non-parallel tests run to completion
// before the parallel batch starts).
func TestMain(m *testing.M) {
	_ = os.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	os.Exit(m.Run())
}
