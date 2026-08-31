package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestToolFallbackAllowlistReroutes verifies the wiring: when the
// KERN_TOOLS allowlist blocks a tool, a call to it reroutes to its
// policy-approved alternative (app.FallbackFor) when that alternative IS
// allowed, instead of hard-failing. kern_what_if → kern_impact (the fallback
// table in internal/app). This is the realistic restricted-deployment case.
func TestToolFallbackAllowlistReroutes(t *testing.T) {
	// Restrict the catalog to kern_impact only: kern_what_if is blocked but
	// its fallback is allowed.
	t.Setenv("KERN_TOOLS", "kern_impact,kern_plan,kern_analyze,kern_verify,kern_validate,kern_loop,kern_heal")
	in, out := newPipe()
	s := NewServer(in, out)

	// A call to the blocked kern_what_if must reroute to kern_impact. With a
	// real repo root, handleImpact runs against the working directory.
	outStr, err := s.runTool(context.Background(), "fallback-test", "kern_what_if",
		map[string]any{"root": ".", "change": "CompileIntent", "kind": "remove_symbol"})
	if err != nil {
		t.Fatalf("runTool(kern_what_if) blocked without fallback: %v", err)
	}
	if strings.TrimSpace(outStr) == "" {
		t.Error("fallback reroute produced empty output")
	}
	if !strings.Contains(outStr, "IMPACT") {
		t.Errorf("fallback output does not look like impact report: %.80q", outStr)
	}
}

// TestToolFallbackFailsClosedWhenAlternativeBlocked verifies that a blocked
// tool whose alternative is ALSO blocked still fails closed (no bypass).
func TestToolFallbackFailsClosedWhenAlternativeBlocked(t *testing.T) {
	// Neither kern_what_if nor its fallback kern_impact is allowed.
	t.Setenv("KERN_TOOLS", "kern_analyze")
	in, out := newPipe()
	s := NewServer(in, out)

	_, err := s.runTool(context.Background(), "fallback-test", "kern_what_if",
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
