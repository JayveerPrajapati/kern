package optimize

import (
	"strings"
	"testing"
)

// TestLogMaskPrivateIP pins mask parity with the MCP surface: optimize.Log
// with Mask must hide a private IP, because --mask is the remote-LLM path.
func TestLogMaskPrivateIP(t *testing.T) {
	res, err := Log("connect ip=192.168.1.42 now", Options{Mask: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "192.168.1.42") {
		t.Fatalf("Log --mask leaked a private IP: %q", res.Output)
	}
	if !strings.Contains(res.Output, "[MASKED_") {
		t.Fatalf("expected a masked placeholder, got %q", res.Output)
	}
}
