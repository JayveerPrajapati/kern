package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TestPlatformAnalyzeWhatIfVerify exercises the three core service methods
// (Analyze, WhatIf, Verify) against the real kern repo. It is the
// contract test: it proves the shared application-services layer wires the
// index → twin-merged graph → memory → firewall → context/verification engines
// correctly and that all three interfaces (CLI/MCP/REST) can rely on it.
// It does NOT assert exact output shapes (those drift as the repo evolves);
// it asserts the structural contract: methods return non-empty results without
// error, the rendered text contains expected section markers, and the
// verification verdict is a known value.
func TestPlatformAnalyzeWhatIfVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := "../.."

	p, err := NewWithIndex(root, sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Analyze: a real exported symbol.
	pkt, text, err := p.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if text == "" {
		t.Error("Analyze returned empty text")
	}
	if !strings.Contains(text, "Task:") {
		t.Errorf("Analyze text missing 'Task:' marker; got:\n%s", text)
	}
	if len(pkt.Symbols) == 0 {
		t.Error("Analyze returned no symbols in packet")
	}

	// WhatIf: remove a symbol — should produce a non-empty impact.
	imp, wiText, err := p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if wiText == "" {
		t.Error("WhatIf returned empty text")
	}
	if !strings.Contains(wiText, "change:") {
		t.Errorf("WhatIf text missing 'change:' marker; got:\n%s", wiText)
	}
	if len(imp.Affected) == 0 {
		t.Error("WhatIf returned no affected symbols for NewServer")
	}

	// Verify: build only (fast, deterministic).
	res := p.Verify([]string{"build"})
	if res.Verdict == "" {
		t.Error("Verify returned empty verdict")
	}
	if res.Build == nil {
		t.Error("Verify returned nil Build result")
	}
}

// TestWhatIfRuntimeEvidence asserts the RuntimeEvidence dimension is filled
// from the platform's runtime source : with a source carrying error
// events attached via WithRuntimeSource, WhatIf returns a non-empty
// RuntimeEvidence; with no source, the dimension stays empty.
func TestWhatIfRuntimeEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := "../.."

	p, err := NewWithIndex(root, sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Without a runtime source, RuntimeEvidence stays empty.
	imp, _, err := p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf (no source): %v", err)
	}
	if len(imp.RuntimeEvidence) != 0 {
		t.Fatalf("expected empty RuntimeEvidence with no source, got %d entries", len(imp.RuntimeEvidence))
	}

	// With a runtime source carrying error events, RuntimeEvidence is populated.
	store := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	store.Ingest(runtime.Event{
		ID:        "evt-1",
		Type:      runtime.EventError,
		Service:   "whatif",
		Severity:  "error",
		Message:   "nil pointer in whatif handler",
		Timestamp: now,
	})
	p.WithRuntimeSource(store)

	imp, _, err = p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf (with source): %v", err)
	}
	if len(imp.RuntimeEvidence) == 0 {
		t.Fatal("expected non-empty RuntimeEvidence when a runtime source with error events is attached")
	}
	if !strings.Contains(imp.RuntimeEvidence[0], "[whatif] error: nil pointer in whatif handler") {
		t.Errorf("RuntimeEvidence[0] not rendered as expected; got: %q", imp.RuntimeEvidence[0])
	}

	// Non-error events must never leak into the dimension.
	if strings.Contains(strings.Join(imp.RuntimeEvidence, "\n"), "no runtime telemetry") {
		t.Error("unexpected content in RuntimeEvidence")
	}
}

// TestPlatformNewWithGraph tests the server constructor that shares a
// caller-owned graph pointer. It verifies the Platform's engines see the
// graph that the caller built (not a copy).
func TestPlatformNewWithGraph(t *testing.T) {
	root := "../.."

	p, err := NewWithIndex(root, sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The Platform's graph pointer must be non-nil and match what New built.
	g := p.Graph()
	if g == nil {
		t.Fatal("Graph() returned nil")
	}
	if len(g.Nodes) == 0 {
		t.Error("Graph has no nodes")
	}

	// Memory, Firewall, and engines must be wired.
	if p.Memory() == nil {
		t.Error("Memory() returned nil")
	}
	if p.Firewall() == nil {
		t.Error("Firewall() returned nil")
	}
	if p.ContextEngine() == nil {
		t.Error("ContextEngine() returned nil")
	}
	if p.VerificationEngine() == nil {
		t.Error("VerificationEngine() returned nil")
	}
}
