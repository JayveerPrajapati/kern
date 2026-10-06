package rulesblock

import (
	"os"
	"strings"
	"testing"
)

// TestExciseKernBlockRealAsset pins the excise against the REAL embedded
// asset (245 lines with INTERIOR global-omit markers at ~38/198/245) —
// synthetic single-marker fixtures missed the interior-anchor case (oracle
// gate finding: an early anchor would leave ~200 kern-rule lines behind as
// "user content" and make every re-wire grow the file).
func TestExciseKernBlockRealAsset(t *testing.T) {
	asset, err := os.ReadFile("../setup/assets/AGENTS.md")
	if err != nil {
		t.Skipf("asset not readable: %v", err)
	}
	user := "\nmy personal notes line A\nmy personal notes line B\n"
	content := string(asset) + user

	got, err := ExciseKernBlock(content)
	if err != nil {
		t.Fatalf("excise on real asset failed: %v", err)
	}
	gotLines := len(strings.Split(got, "\n"))
	if strings.Contains(got, "kern usage rules") || strings.Contains(got, "global-omit") || strings.Contains(got, "KERN_MCP_FULL") {
		t.Fatalf("excise left kern-block lines behind (%d lines remain):\n%.400s", gotLines, got)
	}
	if !strings.Contains(got, "my personal notes line A") || !strings.Contains(got, "my personal notes line B") {
		t.Fatalf("user content after the block was destroyed; got (%d lines):\n%.400s", gotLines, got)
	}
	if gotLines > 6 {
		t.Fatalf("excise must leave only the user content (+leading blank), got %d lines:\n%.400s", gotLines, got)
	}

	// Idempotency: excising the remainder must be a no-op.
	again, err := ExciseKernBlock(got)
	if err != nil {
		t.Fatalf("re-excise failed: %v", err)
	}
	if again != got {
		t.Fatalf("re-excise must be a no-op; before %q after %q", got, again)
	}
}

// TestExciseMarkerBlockWithInteriorHeading pins the marker format whose
// second line is a "# kern usage rules…" heading (the condensed
// global-rules block writes exactly this shape). The interior heading
// must NOT flip the scanner out of marker mode — the block spans exactly
// begin-marker..end-marker (oracle gate finding: Go/awk lacked the mode
// guard install.ps1 has).
func TestExciseMarkerBlockWithInteriorHeading(t *testing.T) {
	marked := "before\n<!-- kern:global-rules begin -->\n# kern usage rules for agents — READ FIRST\nbody line\n<!-- kern:global-rules end -->\nafter\n"
	got, err := ExciseKernBlock(marked)
	if err != nil {
		t.Fatalf("marker block with interior heading must excise cleanly, got error: %v", err)
	}
	if strings.Contains(got, "kern usage rules") || strings.Contains(got, "global-rules begin") || strings.Contains(got, "body line") {
		t.Fatalf("marker block interior leaked into output: %q", got)
	}
	if !strings.HasPrefix(got, "before") || !strings.HasSuffix(got, "after\n") {
		t.Fatalf("content around the marker block must be preserved, got %q", got)
	}
}
