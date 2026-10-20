package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestToolFallbackAllowlistReroutes verifies the wiring: when the
// KERN_TOOLS allowlist blocks a tool, a call to it reroutes to its
// policy-approved alternative (tasklife.FallbackFor) when that alternative IS
// allowed, instead of hard-failing. kern_what_if → kern_impact (the fallback
// table in internal/app). This is the realistic restricted-deployment case.
func TestToolFallbackAllowlistReroutes(t *testing.T) {
	// Restrict the catalog to kern_exec+kern_verify: kern_heal is blocked by
	// the tool allowlist but its fallback (kern_verify) is allowed. The
	// allowlist must name kern_exec so the exec gate passes for the reroute.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_TOOLS", "kern_exec,kern_verify")
	in, out := newPipe()
	s := NewServer(in, out)

	// A call to the blocked kern_heal must reroute to kern_verify. With a
	// real repo root, handleVerify runs against the working directory.
	outStr, err := s.runTool(context.Background(), "fallback-test", "", "kern_heal",
		map[string]any{"root": ".", "task": "verify NewServer"})
	if err != nil {
		t.Fatalf("runTool(kern_heal) blocked without fallback: %v", err)
	}
	if strings.TrimSpace(outStr) == "" {
		t.Error("fallback reroute produced empty output")
	}
}

// TestToolFallbackFailsClosedWhenAlternativeBlocked verifies that a blocked
// tool whose alternative is ALSO blocked still fails closed (no bypass).
func TestToolFallbackFailsClosedWhenAlternativeBlocked(t *testing.T) {
	// Neither kern_heal nor its fallback kern_verify is allowed.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_TOOLS", "kern_search")
	in, out := newPipe()
	s := NewServer(in, out)

	_, err := s.runTool(context.Background(), "fallback-test", "", "kern_heal",
		map[string]any{"root": "."})
	if err == nil {
		t.Fatal("blocked tool with blocked fallback must error")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %q, want allowlist rejection", err)
	}
}

// newPipe returns a reader/writer pair for constructing a Server.
func newPipe() (*bytes.Buffer, *bytes.Buffer) {
	return &bytes.Buffer{}, &bytes.Buffer{}
}
