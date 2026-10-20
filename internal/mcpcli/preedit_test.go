package mcpcli

import "testing"

// TestRunPreEditNoArgsExits2 pins the L1 contract (moved from cmd/kern's
// f10_strict_flags_test.go): a bare `kern pre-edit` is a usage error
// (exit 2), not a runtime failure (exit 1).
func TestRunPreEditNoArgsExits2(t *testing.T) {
	if code := RunPreEdit(nil); code != 2 {
		t.Fatalf("RunPreEdit(nil) = %d, want 2 (usage error)", code)
	}
}
