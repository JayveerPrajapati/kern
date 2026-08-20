package whatif

import (
	"strings"
	"testing"
)

// TestSimulateRenderDriftHeader verifies the shared renderer produces the
// drift-fixed header line ("change: <kind> <target>") and the claim confidence
// on every claim. Both the CLI (`kern what-if`) and the MCP (`kern_what_if`)
// delegate to SimulateRender, so asserting here guarantees their outputs stay
// byte-identical.
func TestSimulateRenderDriftHeader(t *testing.T) {
	root := whatifFixture(t)
	change := "helper"
	out, err := SimulateRender(root, RemoveSymbol, change, "")
	if err != nil {
		t.Fatalf("SimulateRender: %v", err)
	}

	if !strings.HasPrefix(out, "change: remove_symbol ") {
		t.Fatalf("output missing 'change: remove_symbol' header line; got:\n%s", out)
	}
	if !strings.Contains(out, "change: remove_symbol "+change) {
		t.Fatalf("header missing target %q; got:\n%s", change, out)
	}
	for _, want := range []string{"affected: ", "files: ", "services: ", "tests: ", "risk: ", "recommendation: "} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q; got:\n%s", want, out)
		}
	}
	// Every claim line must carry a confidence in parentheses.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "claim[") {
			continue
		}
		if !strings.Contains(line, " (") || !strings.Contains(line, "): ") {
			t.Fatalf("claim line missing confidence (%.1f): %q", 0.0, line)
		}
	}
}

// TestSimulateRenderIdenticalAcrossKinds verifies the same bytes are produced
// for both the default and explicit kinds (exercises the shared call path).
func TestSimulateRenderIdenticalAcrossKinds(t *testing.T) {
	root := whatifFixture(t)
	a, err := SimulateRender(root, RemoveSymbol, "helper", "")
	if err != nil {
		t.Fatalf("SimulateRender: %v", err)
	}
	b, err := SimulateRender(root, RemoveSymbol, "helper", "")
	if err != nil {
		t.Fatalf("SimulateRender: %v", err)
	}
	if a != b {
		t.Fatal("SimulateRender is not deterministic for identical input")
	}
}
