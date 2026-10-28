package main

import (
	"strings"
	"testing"
)

// TestOptimizePromptMaskPrivateIP pins P2-4 (2026-10-28 adversarial audit):
// `kern optimize --mask` used the local-context mask (which deliberately
// leaves private IPs alone), while the MCP mask surface masked them. --mask
// protects text sent to a remote LLM, so both must hide `ip=192.168.1.42`.
func TestOptimizePromptMaskPrivateIP(t *testing.T) {
	root := newRoot(t)
	_ = root
	// Force the deterministic path: a reachable local LLM would rewrite the
	// prompt and the assertions below are about the re-mask, not the model.
	t.Setenv("KERN_LLM_PROVIDER", "none")
	out := captureSetupOut(t, func() {
		runOptimize("optimize", []string{"--mask", "connect ip=192.168.1.42 now"})
	})
	if strings.Contains(out, "192.168.1.42") {
		t.Fatalf("optimize --mask leaked a private IP: %q", out)
	}
	if !strings.Contains(out, "[MASKED_") {
		t.Fatalf("expected a masked placeholder, got %q", out)
	}
}
