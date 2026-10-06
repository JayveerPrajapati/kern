package tasklife

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// resolveOnlyPlatform is a minimal PlatformAPI stub for the entry-parity test:
// resolveSymbol's single-token path touches only Index() and Graph(), so the
// heavy platform wiring (twin merge, memory, firewall, context engine,
// verification) is skipped — the test cost stays at the index build + graph
// promotion, the same profile as the intel parity test (index.Build + FromIndex).
// The remaining methods return zero values and are never reached.
type resolveOnlyPlatform struct {
	root  string
	ix    *index.Index
	graph *intel.Graph
}

func (p *resolveOnlyPlatform) Root() string                   { return p.root }
func (p *resolveOnlyPlatform) Index() *index.Index            { return p.ix }
func (p *resolveOnlyPlatform) Graph() *intel.Graph            { return p.graph }
func (p *resolveOnlyPlatform) Memory() *memory.MemoryStore    { return nil }
func (p *resolveOnlyPlatform) Firewall() *governance.Firewall { return nil }
func (p *resolveOnlyPlatform) RuntimeSource() runtime.Source  { return nil }
func (p *resolveOnlyPlatform) ContextEngine() *context.Engine { return nil }
func (p *resolveOnlyPlatform) Analyze(change string) (domain.ContextPacket, string, error) {
	return domain.ContextPacket{}, "", nil
}
func (p *resolveOnlyPlatform) Risk(change string) (domain.ContextPacket, string, error) {
	return domain.ContextPacket{}, "", nil
}
func (p *resolveOnlyPlatform) WhatIf(kind whatif.ChangeKind, change, newTarget string) (whatif.Impact, string, error) {
	return whatif.Impact{}, "", nil
}
func (p *resolveOnlyPlatform) Verify(types []string, opts ...verification.Option) verdict.VerificationResult {
	return verdict.VerificationResult{}
}
func (p *resolveOnlyPlatform) CorrelateCode(alert domain.Alert) (Correlation, error) {
	return Correlation{}, nil
}
func (p *resolveOnlyPlatform) CodeContext(intent, plan string) (string, error) {
	return "", nil
}

// TestImpactExploreEntryResolutionParity pins the P2 entry-level agreement
// between kern impact (tasklife.resolveSymbol) and kern explore
// (intel.ResolveEntry) on a REAL index: the same ambiguous hub name must
// resolve to the SAME final definition on both surfaces, and while any
// production definition exists the resolution must NEVER be a test/fixture
// symbol (the live store.New bug — impact resolved "New" to a fixture under
// evaluate/*/fixture/ instead of the production app.New). It mirrors the
// intel parity test's index.Build("../..") sampling pattern; the stub
// platform keeps the cost to the index build + graph promotion.
func TestImpactExploreEntryResolutionParity(t *testing.T) {
	// "../.." is the repo root from the tasklife package dir: a real index
	// with the same-named hubs the live divergence was observed on ("New"
	// with ~23 definitions, "Run" with ~8).
	ix, err := index.Build("../..")
	if err != nil {
		t.Fatalf("index.Build(../..): %v", err)
	}
	g := intel.FromIndex(ix)
	p := &resolveOnlyPlatform{root: "../..", ix: ix, graph: &g}

	for _, name := range []string{"New", "Run"} {
		// Precondition: the hub name must actually be ambiguous in this tree.
		// If the repo evolves so it is not, skip rather than fail — the parity
		// pin needs a multi-definition hub.
		if defs := g.DefsWithSimpleName(name); len(defs) < 2 {
			t.Skipf("%q is no longer ambiguous in this tree (%d defs); entry-parity pin needs a multi-definition hub", name, len(defs))
		}

		// intel's shared entry resolver (kern explore's entry).
		resolved, d, ok := intel.ResolveEntry(ix, name)
		if !ok || d == nil {
			t.Fatalf("ResolveEntry(%q): not resolved", name)
		}

		// tasklife's resolver (kern impact's entry).
		target, fuzzy, err := resolveSymbol(p, name)
		if err != nil {
			t.Fatalf("resolveSymbol(%q): %v", name, err)
		}
		if !fuzzy {
			t.Errorf("resolveSymbol(%q) = %q: expected fuzzy=true for an ambiguous hub", name, target)
		}

		// SAME final resolved symbol: the target's graph node must be the
		// definition ResolveEntry picked.
		node, ok := g.NodeByID(target)
		if !ok || node.Symbol == nil {
			t.Fatalf("resolveSymbol(%q) = %q: no graph node for the resolved target", name, target)
		}
		if node.Symbol.File != d.File || node.Symbol.Line != d.Line {
			t.Errorf("entry divergence for %q: tasklife %q -> %s:%d, intel ResolveEntry -> %q (%s:%d)",
				name, target, node.Symbol.File, node.Symbol.Line, resolved, d.File, d.Line)
		}

		// Regression pin: while any production definition exists, the
		// resolution is NEVER a test/fixture symbol (the live store.New bug).
		if strings.Contains(d.File, "/fixture/") || strings.Contains(d.File, "/fixtures/") ||
			strings.Contains(d.File, "/testdata/") || strings.HasSuffix(d.File, "_test.go") {
			t.Errorf("resolve %q -> %s:%d; must never be a test/fixture symbol while production definitions exist", name, d.File, d.Line)
		}
	}
}
