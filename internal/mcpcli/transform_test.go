package mcpcli

import "testing"

// TestRunAstTransformBadFlagExits2 pins the uniform flag-error handling: a
// bad flag exits 2 with a "flags:" usage error, never a silent default.
func TestRunAstTransformBadFlagExits2(t *testing.T) {
	if code := RunAstTransform([]string{"--bogus"}); code != 2 {
		t.Fatalf("RunAstTransform(--bogus) = %d, want 2 (usage error)", code)
	}
}
