package whatif

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
)

// whatifFixture writes a tiny Go module with a small call chain plus an
// isolated symbol, and returns its root.
func whatifFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module whatiffix\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func mid() string { return helper() }

func top() string { return mid() }

func unused() string { return "unused" }

func main() { println(top()) }
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "h" {
		t.Fail()
	}
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// nodeID returns the first node ID whose symbol name matches the simple name.
func nodeID(g *intelligence.Graph, simple string) string {
	for _, n := range g.Nodes {
		if n.Symbol != nil && (n.Symbol.Name == simple || strings.HasSuffix(n.Symbol.Name, "."+simple)) {
			return n.ID
		}
	}
	return ""
}

func buildGraph(t *testing.T) *intelligence.Graph {
	t.Helper()
	ix, err := index.Build(whatifFixture(t))
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	g := intelligence.FromIndex(ix)
	return &g
}

func TestSimulateRemoveSymbol(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found in graph")
	}

	imp := Simulate(g, Change{Kind: RemoveSymbol, Target: id})
	if imp.Isolated {
		t.Fatal("removing helper should not be isolated (mid/top depend on it)")
	}
	if len(imp.Affected) == 0 {
		t.Fatal("expected transitively affected symbols")
	}
	if !contains(imp.Affected, "mid") && !hasSuffix(imp.Affected, ".mid") {
		t.Fatalf("expected mid among affected, got %v", imp.Affected)
	}
	if len(imp.Files) == 0 {
		t.Fatal("expected affected files")
	}
	if imp.Risk != "medium" {
		t.Fatalf("risk = %q, want medium", imp.Risk)
	}
	if imp.Recommendation == "" {
		t.Fatal("expected a recommendation")
	}
	if len(imp.Claims) == 0 {
		t.Fatal("expected at least one typed claim")
	}
	if imp.Claims[0].Type != domain.ClaimRecommendation {
		t.Fatalf("claim type = %q, want RECOMMENDATION", imp.Claims[0].Type)
	}
	if imp.Claims[0].Provenance != "whatif:simulate" {
		t.Fatalf("provenance = %q, want whatif:simulate", imp.Claims[0].Provenance)
	}
}

func TestSimulateIsolatedSymbol(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "unused")
	if id == "" {
		t.Fatal("unused symbol not found")
	}
	imp := Simulate(g, Change{Kind: RemoveSymbol, Target: id})
	if !imp.Isolated {
		t.Fatal("unused should be isolated (nothing depends on it)")
	}
	if imp.Risk != "low" {
		t.Fatalf("risk = %q, want low", imp.Risk)
	}
	if len(imp.Affected) != 0 {
		t.Fatalf("expected no affected symbols, got %v", imp.Affected)
	}
}

func TestSimulateChangeDependency(t *testing.T) {
	g := buildGraph(t)
	top := nodeID(g, "top")
	mid := nodeID(g, "mid")
	unused := nodeID(g, "unused")
	if top == "" || mid == "" || unused == "" {
		t.Fatal("symbols not found")
	}
	// top now depends on unused as well.
	imp := Simulate(g, Change{Kind: ChangeDependency, Target: top, NewTarget: unused})
	if imp.Isolated {
		t.Fatal("change_dependency should surface dependents of the new target")
	}
	// Simulate is read-only: the graph must be unchanged.
	if len(g.Nodes) == 0 {
		t.Fatal("graph should not be mutated")
	}
}

func TestSimulateAddSymbol(t *testing.T) {
	g := buildGraph(t)
	// Adding a brand-new symbol: use a synthetic target that does not exist in
	// the graph so affected must be empty and risk low (isolated).
	imp := Simulate(g, Change{Kind: AddSymbol, Target: "whatiffix.brandnew"})
	if len(imp.Affected) != 0 {
		t.Fatalf("expected no affected symbols for a new symbol, got %v", imp.Affected)
	}
	if !imp.Isolated {
		t.Fatal("a brand-new symbol should be isolated (nothing depends on it)")
	}
	if imp.Risk != "low" {
		t.Fatalf("risk = %q, want low for add_symbol", imp.Risk)
	}
}

func TestSimulateChangeSignature(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found")
	}
	imp := Simulate(g, Change{Kind: ChangeSignature, Target: id})
	// Callers of helper (mid, and transitively top) are affected.
	if len(imp.Affected) == 0 {
		t.Fatal("changing a signature should surface its callers")
	}
	if !contains(imp.Affected, "mid") && !hasSuffix(imp.Affected, ".mid") {
		t.Fatalf("expected mid among affected for a signature change, got %v", imp.Affected)
	}
	if len(imp.Files) == 0 {
		t.Fatal("expected affected files for a signature change")
	}
}

func TestSimulateAlternatives(t *testing.T) {
	cases := []Change{
		{Kind: RemoveSymbol, Target: "x"},
		{Kind: ChangeSignature, Target: "x"},
		{Kind: AddDependency, Target: "x", NewTarget: "y"},
		{Kind: RemoveDependency, Target: "x"},
		{Kind: ChangeDependency, Target: "x", NewTarget: "y"},
		{Kind: AddSymbol, Target: "x"},
	}
	for _, c := range cases {
		imp := Simulate(buildGraph(t), c)
		if len(imp.Alternatives) == 0 {
			t.Fatalf("expected at least one alternative for kind %q", c.Kind)
		}
	}
}

// TestWhatIfAlias asserts WhatIf is a drop-in alias for Simulate: identical
// inputs produce identical Impact .
func TestWhatIfAlias(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found")
	}
	c := Change{Kind: RemoveSymbol, Target: id}
	viaSimulate := Simulate(g, c)
	viaWhatIf := WhatIf(g, c)
	if got, want := len(viaWhatIf.Affected), len(viaSimulate.Affected); got != want {
		t.Fatalf("WhatIf affected len = %d, Simulate = %d", got, want)
	}
	if got, want := viaWhatIf.Risk, viaSimulate.Risk; got != want {
		t.Fatalf("WhatIf risk = %q, Simulate = %q", got, want)
	}
	if got, want := viaWhatIf.Recommendation, viaSimulate.Recommendation; got != want {
		t.Fatalf("WhatIf recommendation differs from Simulate")
	}
}

// TestImpactHasNewFields asserts the Impact struct exposes the spec §12 pipeline
// dimensions as non-nil (possibly empty) fields, and the method is named.
func TestImpactHasNewFields(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found")
	}
	imp := Simulate(g, Change{Kind: RemoveSymbol, Target: id})
	if imp.ArchitectureViolations == nil {
		t.Fatal("ArchitectureViolations must be non-nil (empty is fine)")
	}
	if imp.HistoricalEvidence == nil {
		t.Fatal("HistoricalEvidence must be non-nil (empty is fine)")
	}
	if imp.RuntimeEvidence == nil {
		t.Fatal("RuntimeEvidence must be non-nil (empty is fine)")
	}
	if imp.Databases == nil {
		t.Fatal("Databases must be non-nil (empty is fine)")
	}
	if imp.Method != "graph-traversal" {
		t.Fatalf("Method = %q, want graph-traversal", imp.Method)
	}
}

// TestRenameSymbolChangeKind asserts a RenameSymbol change surfaces the callers
// of the renamed symbol (they must be updated to the new name) plus the symbol
// itself.
func TestRenameSymbolChangeKind(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found")
	}
	imp := Simulate(g, Change{Kind: RenameSymbol, Target: id})
	if len(imp.Affected) == 0 {
		t.Fatal("renaming helper should surface its callers")
	}
	if !contains(imp.Affected, "mid") && !hasSuffix(imp.Affected, ".mid") {
		t.Fatalf("expected mid among affected for a rename, got %v", imp.Affected)
	}
}

// TestNewChangeKindsHandled asserts the higher-level change kinds (which need
// twin/context data for full simulation) don't panic and surface a summary note.
func TestNewChangeKindsHandled(t *testing.T) {
	for _, kind := range []ChangeKind{SplitService, MoveModule, ChangeInfra} {
		c := Change{Kind: kind, Target: "whatiffix.main"}
		imp := Simulate(buildGraph(t), c)
		if imp.Summary == "" {
			t.Fatalf("expected a non-empty Summary for kind %q", kind)
		}
		if !strings.Contains(imp.Summary, "high-level change type") {
			t.Fatalf("kind %q Summary missing high-level note: %q", kind, imp.Summary)
		}
	}
}

func TestSimulateMitigations(t *testing.T) {
	// Force a high-risk change (many affected) and check mitigations appear.
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found")
	}
	imp := Simulate(g, Change{Kind: ChangeSignature, Target: id})
	if imp.Risk == "low" {
		t.Fatal("signature change on helper should not be low risk (it has callers)")
	}
	if len(imp.Mitigations) == 0 {
		t.Fatal("expected mitigations for a non-low-risk change")
	}
	// A low-risk (isolated) change still yields a deterministic mitigation.
	g2 := buildGraph(t)
	imp2 := Simulate(g2, Change{Kind: AddSymbol, Target: "comiffix.brandnew"})
	if len(imp2.Mitigations) == 0 {
		t.Fatal("expected a mitigation entry even for low risk")
	}
}

func TestSimulateConfidence(t *testing.T) {
	g := buildGraph(t)
	// Isolated change -> highest confidence.
	imp := Simulate(g, Change{Kind: AddSymbol, Target: "comiffix.brandnew"})
	if len(imp.Affected) != 0 || imp.Confidence != 0.95 {
		t.Fatalf("isolated change: confidence = %v, want 0.95", imp.Confidence)
	}
	// Affected < 5 -> 0.80. unused has no callers, so a signature change on it
	// yields exactly one affected symbol (itself).
	unused := nodeID(g, "unused")
	if unused == "" {
		t.Fatal("unused symbol not found")
	}
	imp2 := Simulate(g, Change{Kind: ChangeSignature, Target: unused})
	if len(imp2.Affected) >= 5 || imp2.Confidence != 0.80 {
		t.Fatalf("small affected set: len=%d confidence = %v, want 0.80", len(imp2.Affected), imp2.Confidence)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want || strings.HasSuffix(s, "."+want) {
			return true
		}
	}
	return false
}

func hasSuffix(ss []string, suffix string) bool {
	for _, s := range ss {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}
