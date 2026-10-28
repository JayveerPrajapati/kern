package index

import (
	"testing"
	"time"
)

// TestGateTTLDefault pins the stat-gate backstop default: 5 minutes since
// the 2026-10-27 idle-tax fix (was 30 s). The backstop is how long a
// retained, stat-current content walk may decide freshness without a full
// git/content re-derivation; lowering it back to seconds would re-introduce
// the measured ~0.6-0.7 s re-proof on the first daemon query after every
// agent thinking/editing pause.
func TestGateTTLDefault(t *testing.T) {
	t.Setenv("KERN_FRESHNESS_GATE_MS", "")
	if got := gateTTL(); got != 5*time.Minute {
		t.Fatalf("gateTTL() default = %v, want 5m", got)
	}
	t.Setenv("KERN_FRESHNESS_GATE_MS", "1500")
	if got := gateTTL(); got != 1500*time.Millisecond {
		t.Fatalf("gateTTL() override = %v, want 1.5s", got)
	}
}
