package mcpcli

import "testing"

// TestRunEvidenceAnchorBareExits1 pins the runtime-error contract: an
// evidence-anchor with no claim/file/symbol fails with exit 1 (the tool
// body rejects the empty anchor before any index work).
func TestRunEvidenceAnchorBareExits1(t *testing.T) {
	if code := RunEvidenceAnchor(nil); code != 1 {
		t.Fatalf("RunEvidenceAnchor(nil) = %d, want 1 (runtime error)", code)
	}
}
