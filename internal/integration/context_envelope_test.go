package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// writeFixture writes a minimal 2-file Go fixture repo into dir. The fixture
// is deliberately tiny (two files, four functions) so index.Build stays well
// under a second. Call graph: Run -> Count -> tick. The file is named
// calc.go (not counter.go) so an L1 query for "Count" matches exactly one
// symbol — a file named counter.go would also match via its path.
func writeFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"calc.go": `package fixture

// tick increments n by one and returns the new total.
func tick(n int) int {
	return n + 1
}

// Count returns the total after n ticks.
func Count(n int) int {
return tick(n)
}

// Sum returns the accumulated total of n Count calls.
func Sum(n int) int {
total := 0
for i := 0; i < n; i++ {
total += Count(3)
}
return total
}
`,
		"main.go": `package fixture
// Run counts three ticks and returns the result.
func Run() int {
return Count(3)
}

// Report returns the Count total for two rounds of ticks.
func Report() int {
return Count(2)
}


// Describe returns a fixed description of the fixture.
func Describe() string {
	return "fixture counter"
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// buildFixtureIndex builds a real symbol index over a fresh 2-file fixture
// repo in t.TempDir(). The index is shared by several integration tests.
func buildFixtureIndex(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("index.Build(%s): %v", dir, err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatalf("fixture index built with 0 symbols")
	}
	return ix
}

// newContextEngine wires the real context engine to a fixture index: graph
// from the index, an on-disk memory store, and a firewall with the engine's
// own agent registered (source write) so risk assessment runs cleanly.
func newContextEngine(t *testing.T, ix *index.Index) *context.Engine {
	t.Helper()
	g := intelligence.FromIndex(ix)
	mem := memory.NewMemoryStore(t.TempDir())
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		"context-engine", "Context Engine", "planner",
		[]governance.Permission{{Resource: "source", Action: "write"}},
	))
	return context.NewEngine(ix.Root, &g, mem, fw)
}

// TestContextEnvelope assembles a ContextPacket end-to-end from a real index
// and exercises the envelope lifecycle: planner stamping, Validate, Migrate,
// RenderText, and budget fitting.
func TestContextEnvelope(t *testing.T) {
	ix := buildFixtureIndex(t)
	eng := newContextEngine(t, ix)

	pkt, err := eng.AnalyzeChange("Count")
	if err != nil {
		t.Fatalf("AnalyzeChange(Count): %v", err)
	}

	// Full envelope flow: engine packet -> planner stamps the envelope
	// (EnvelopeVersionV1 + SchemaVersion) -> Validate -> Migrate (no-op).
	context.PlanPacket(&pkt, "fix the Count panic", 0)

	if pkt.EnvelopeVersion != domain.EnvelopeVersionV1 {
		t.Errorf("EnvelopeVersion = %d, want %d (stamped by the planner)",
			pkt.EnvelopeVersion, domain.EnvelopeVersionV1)
	}
	if pkt.SchemaVersion != "1.0.0" {
		t.Errorf("SchemaVersion = %q, want 1.0.0", pkt.SchemaVersion)
	}
	if err := pkt.Validate(); err != nil {
		t.Errorf("Validate() on a v1 packet: %v", err)
	}

	// Migrate is a no-op on the current version: nil error, no mutation.
	if err := pkt.Migrate(); err != nil {
		t.Errorf("Migrate() on a v1 packet: %v", err)
	}
	if pkt.EnvelopeVersion != domain.EnvelopeVersionV1 {
		t.Errorf("Migrate mutated the envelope version to %d", pkt.EnvelopeVersion)
	}

	// Validate rejects a bogus (future) version.
	bad := pkt
	bad.EnvelopeVersion = domain.EnvelopeVersionV1 + 1
	if err := bad.Validate(); err == nil {
		t.Error("Validate() accepted EnvelopeVersionV1+1, want rejection")
	}

	// RenderText produces non-empty text naming the target symbol.
	text := context.RenderText(pkt)
	if strings.TrimSpace(text) == "" {
		t.Fatal("RenderText returned empty text")
	}
	if !strings.Contains(text, "Count") {
		t.Errorf("RenderText missing target symbol %q: %q", "Count", text)
	}

	// Full flow: packet -> RenderText -> budget.Fit still carries the symbol.
	fitted := budget.Fit(text, 2000)
	if fitted == "" {
		t.Error("budget.Fit returned empty text")
	}
	if !strings.Contains(fitted, "Count") {
		t.Errorf("budget.Fit(RenderText(pkt)) lost target symbol %q: %q", "Count", fitted)
	}
}
