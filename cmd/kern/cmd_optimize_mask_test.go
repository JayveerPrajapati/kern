package main

import (
	"strings"
	"testing"
)

// TestOptimizeLogKindMask (QA 2026-10-19): `kern optimize --kind log --mask`
// emitted every secret verbatim — the --mask flag was not wired on the log
// path (runLog never passed Mask to optimize.Log and never re-masked the
// output). The exact reproduction input must come out fully masked.
func TestOptimizeLogKindMask(t *testing.T) {
	root := newRoot(t)
	_ = root // isolate stats/cache under a temp XDG dir
	in := "ERROR auth failed key=AKIA1234567890EXAMPLE token=ghp_abc123def456ghi user=qa.tester@example-corp.com retrying"
	out := captureSetupOut(t, func() {
		runLog([]string{"--mask", in})
	})
	for _, leak := range []string{"AKIA1234567890EXAMPLE", "ghp_abc123def456ghi", "qa.tester@example-corp.com"} {
		if strings.Contains(out, leak) {
			t.Errorf("optimize --kind log --mask leaked %q in output: %q", leak, out)
		}
	}
	if !strings.Contains(out, "[MASKED_") {
		t.Errorf("expected masked placeholders in log output, got %q", out)
	}
}
