package mcpcli

import "testing"

// TestRunComposeNoPipelineExits2 pins the usage contract: a compose
// invocation with no pipeline input (no --pipeline, no positional JSON, no
// stdin) is a usage error (exit 2).
func TestRunComposeNoPipelineExits2(t *testing.T) {
	if code := RunCompose(nil); code != 2 {
		t.Fatalf("RunCompose(nil) = %d, want 2 (usage error)", code)
	}
}
