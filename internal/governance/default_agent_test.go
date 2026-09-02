package governance

import (
	"os"
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
