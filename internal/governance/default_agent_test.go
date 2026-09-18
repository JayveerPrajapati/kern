package governance

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestPermissiveMode(t *testing.T) {
	cases := map[string]bool{
		"1":        true,
		"true":     true,
		"TRUE":     true,
		"True":     true,
		"":         false,
		"0":        false,
		"false":    false,
		"yes":      false,
		"anything": false,
	}
	for val, want := range cases {
		t.Setenv("KERN_MCP_PERMISSIVE", val)
		if got := PermissiveMode(); got != want {
			t.Fatalf("PermissiveMode() with %q = %v, want %v", val, got, want)
		}
	}
}

func TestEnsureDefaultAgentIdempotent(t *testing.T) {
	EnsureDefaultAgent()
	a, err := GetAgent(DefaultAgentID)
	if err != nil {
		t.Fatalf("default agent not registered: %v", err)
	}
	if a.ID != DefaultAgentID {
		t.Fatalf("agent id = %q, want %q", a.ID, DefaultAgentID)
	}
	// Re-entry after a prior registration is a no-op, not an error.
	EnsureDefaultAgent()
	if _, err := GetAgent(DefaultAgentID); err != nil {
		t.Fatalf("default agent lost after re-registration: %v", err)
	}
	if os.Getenv("KERN_MCP_PERMISSIVE") == "1" && !PermissiveMode() {
		t.Fatal("PermissiveMode inconsistent with env")
	}
}

// TestPermissiveActiveAndWarning locks audit A1: the active permissive state
// is exposed via PermissiveActive and the first activation emits the one-time
// warning to stderr.
func TestPermissiveActiveAndWarning(t *testing.T) {
	t.Setenv("KERN_MCP_PERMISSIVE", "")
	if PermissiveActive() {
		t.Fatal("PermissiveActive() should be false when KERN_MCP_PERMISSIVE is unset")
	}

	// Capture stderr around an activation. The once is reset so this test
	// observes the warning deterministically regardless of test order.
	permissiveWarnOnce = sync.Once{}
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Setenv("KERN_MCP_PERMISSIVE", "1")
	active := PermissiveActive()
	w.Close()
	os.Stderr = oldStderr
	buf, _ := io.ReadAll(r)
	if !active {
		t.Fatal("PermissiveActive() should be true with KERN_MCP_PERMISSIVE=1")
	}
	if !strings.Contains(string(buf), permissiveWarning) {
		t.Fatalf("activation should emit the one-time warning, stderr = %q", string(buf))
	}

	// The warning fires at most once: a second observation writes nothing.
	permissiveWarnOnce = sync.Once{}
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w2
	PermissiveActive()
	PermissiveActive()
	w2.Close()
	os.Stderr = oldStderr
	buf2, _ := io.ReadAll(r2)
	if strings.Count(string(buf2), "WARNING: KERN_MCP_PERMISSIVE") > 1 {
		t.Fatalf("warning must be emitted at most once, got %q", string(buf2))
	}
}
