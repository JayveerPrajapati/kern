package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

func sym(kind, name, file string, line int) index.Symbol {
	return index.Symbol{Kind: kind, Name: name, File: file, Line: line, Lang: "go"}
}

// fakeIndex builds a small in-memory v1 index:
//
//	Foo -> Bar, Baz
//	Bar -> Baz
//	HandleUsers -> Foo    (entry point, Framework "net-http", Route "/users")
//	TestFoo -> HandleUsers
//	Baz inherits "extends:Animal"
//	svc imports "net/http"
func fakeIndex() *index.Index {
	return &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			sym("func", "Foo", "foo.go", 1),
			sym("func", "Bar", "bar.go", 1),
			sym("func", "Baz", "baz.go", 1),
			{Kind: "func", Name: "HandleUsers", File: "svc/handlers.go", Line: 1, Lang: "go", Entry: true, Framework: "net-http", Route: "/users"},
			sym("func", "TestFoo", "svc/foo_test.go", 1),
		},
		Calls: map[string][]string{
			"Foo":         {"Bar", "Baz"},
			"Bar":         {"Baz"},
			"HandleUsers": {"Foo"},
			"TestFoo":     {"HandleUsers"},
		},
		Callers: map[string][]string{
			"Bar":         {"Foo"},
			"Baz":         {"Foo", "Bar"},
			"Foo":         {"HandleUsers"},
			"HandleUsers": {"TestFoo"},
		},
		Inherits: map[string][]string{
			"Baz": {"extends:Animal"},
		},
		InheritedBy: map[string][]string{
			"Animal": {"Baz"},
		},
		Pkgs: map[string]*index.Pkg{
			"svc": {Name: "svc", Path: "svc", Imports: []string{"net/http"}, Files: []string{"svc/handlers.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// testEngine builds an Engine wired to an in-memory graph, memory store, and
// firewall, pre-seeded with a lesson and an incident about the "Foo" scope.
func testEngine(t *testing.T) *Engine {
	t.Helper()

	g := intelligence.FromIndex(fakeIndex())

	mem := memory.NewMemoryStore(t.TempDir())
	if _, err := mem.Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: "Foo must not return nil for empty input",
		Scope:   "Foo",
		Source:  "human",
	}); err != nil {
		t.Fatalf("add lesson: %v", err)
	}
	if _, err := mem.Add(domain.Memory{
		Type:    domain.MemoryIncident,
		Content: "Previous change to Foo caused a regression in HandleUsers",
		Scope:   "Foo",
		Source:  "sre",
	}); err != nil {
		t.Fatalf("add incident: %v", err)
	}

	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		engineAgent, "Context Engine", "planner",
		[]governance.Permission{{Resource: "source", Action: "write"}},
	))

	return NewEngine("/fake", &g, mem, fw)
}

func TestNewEngine(t *testing.T) {
	e := testEngine(t)
	if e == nil || e.graph == nil {
		t.Fatal("NewEngine returned nil engine or nil graph")
	}
}

func TestAnalyzeChangePopulatesAllFields(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo) error: %v", err)
	}

	// Symbols: target + direct callers (HandleUsers) + direct callees (Bar, Baz).
	syms := symbolNames(pkt.Symbols)
	if !containsStr(syms, "Foo") {
		t.Errorf("symbols missing target Foo: %v", syms)
	}
	if !containsStr(syms, "HandleUsers") {
		t.Errorf("symbols missing direct caller HandleUsers: %v", syms)
	}
	if !containsStr(syms, "Bar") || !containsStr(syms, "Baz") {
		t.Errorf("symbols missing direct callees Bar/Baz: %v", syms)
	}

	// Files: the target's file must be present.
	if !containsFile(pkt.Files, "foo.go") {
		t.Errorf("files missing foo.go: %v", fileNames(pkt.Files))
	}

	// Memory: at least one recalled entry (lesson) plus the incident.
	if len(pkt.Memory) == 0 {
		t.Error("Memory empty; expected at least the recalled lesson")
	}
	if len(pkt.Incidents) == 0 {
		t.Error("Incidents empty; expected the stored incident")
	}

	// Risks: the firewall assessed source:write.
	if len(pkt.Risks) == 0 {
		t.Error("Risks empty; expected an assessed risk")
	}

	// Facts: includes the graph impact claim.
	if !hasGraphImpact(pkt.Facts) {
		t.Error("Facts missing graph impact claim")
	}

	// Facts: includes a recommendation claim.
	if !hasRecommendation(pkt.Facts) {
		t.Error("Facts missing recommendation claim")
	}

	// RequiredValidation is non-empty and suggests tests/build.
	if len(pkt.RequiredValidation) == 0 {
		t.Error("RequiredValidation is empty")
	}

	// TokenCount > 0.
	if pkt.TokenCount <= 0 {
		t.Errorf("TokenCount = %d, want > 0", pkt.TokenCount)
	}

	// GeneratedAt set.
	if pkt.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}
}

func TestAnalyzeChangeRequiresValidationSuggestsTests(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Bar")
	if err != nil {
		t.Fatalf("AnalyzeChange(Bar) error: %v", err)
	}
	if len(pkt.RequiredValidation) == 0 {
		t.Fatal("RequiredValidation empty")
	}
	if !containsStr(pkt.RequiredValidation, "build verification") {
		t.Errorf("RequiredValidation missing build verification: %v", pkt.RequiredValidation)
	}
}

func TestAnalyzeChangeUnknownSymbol(t *testing.T) {
	e := testEngine(t)
	_, err := e.AnalyzeChange("DoesNotExist")
	if err == nil {
		t.Fatal("AnalyzeChange(DoesNotExist) expected an error, got nil")
	}
}

func TestAnalyzeFile(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeFile("foo.go")
	if err != nil {
		t.Fatalf("AnalyzeFile(foo.go) error: %v", err)
	}
	if len(pkt.Symbols) == 0 {
		t.Error("AnalyzeFile produced no symbols")
	}
	if !containsFile(pkt.Files, "foo.go") {
		t.Errorf("AnalyzeFile files missing foo.go: %v", fileNames(pkt.Files))
	}
}

func TestAnalyzeFileUnknown(t *testing.T) {
	e := testEngine(t)
	_, err := e.AnalyzeFile("missing.go")
	if err == nil {
		t.Fatal("AnalyzeFile(missing.go) expected an error, got nil")
	}
}

func TestMeasure(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo) error: %v", err)
	}
	m := Measure(pkt, 5*time.Millisecond)
	if m.TokenReduction < 0 {
		t.Errorf("TokenReduction = %v, want >= 0", m.TokenReduction)
	}
	if m.TokenReduction <= 0 {
		t.Errorf("TokenReduction = %v, want > 0 given raw ~4x packet", m.TokenReduction)
	}
	if m.Latency != 5*time.Millisecond {
		t.Errorf("Latency = %v, want 5ms", m.Latency)
	}
	if m.RetrievalRelevance < 0 || m.RetrievalRelevance > 100 {
		t.Errorf("RetrievalRelevance = %v, want in [0,100]", m.RetrievalRelevance)
	}
	if m.Cost < 0 {
		t.Errorf("Cost = %v, want >= 0", m.Cost)
	}
}

func TestTokenCountDeterministic(t *testing.T) {
	e := testEngine(t)
	a, _ := e.AnalyzeChange("Foo")
	b, _ := e.AnalyzeChange("Foo")
	if a.TokenCount != b.TokenCount {
		t.Errorf("TokenCount not deterministic: %d != %d", a.TokenCount, b.TokenCount)
	}
}

func TestAnalyzeChangeWithMaxTokens(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo) error: %v", err)
	}
	if pkt.FittedText != "" {
		t.Errorf("FittedText should be empty without MaxTokens (backward compat), got non-empty")
	}

	// Use a budget small enough to force fitting (below the ~1195 JSON count)
	// but large enough that the fitted render text stays non-empty.
	small := 400
	e = testEngine(t).WithMaxTokens(small)
	pkt, err = e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo) with MaxTokens error: %v", err)
	}
	if pkt.FittedText == "" {
		t.Fatalf("FittedText empty with WithMaxTokens(%d); expected budgeted text", small)
	}
	if pkt.TokenCount > small {
		t.Errorf("TokenCount = %d, want <= %d after fitting", pkt.TokenCount, small)
	}
}

// --- helpers ---

func symbolNames(syms []domain.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Qualified
	}
	return out
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func fileNames(files []domain.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func containsFile(files []domain.File, want string) bool {
	for _, f := range files {
		if f.Path == want {
			return true
		}
	}
	return false
}

func hasGraphImpact(facts []domain.Claim) bool {
	for _, c := range facts {
		if c.Source == "intel" && c.Type == domain.ClaimInference {
			return true
		}
	}
	return false
}

func hasRecommendation(facts []domain.Claim) bool {
	for _, c := range facts {
		if c.Type == domain.ClaimRecommendation && c.Provenance == "context:recommendation" {
			return true
		}
	}
	return false
}

// TestContextPacketNoNilFields guards against the carried-forward stubs
// (FIX #10a): ArchitectureRules and RuntimeEvidence must NEVER be nil after a
// packet is assembled. They may be empty (no validator/runtime source wired, or
// nothing matched), but consumers must be able to rely on len() == 0 rather than
// a nil check.
func TestContextPacketNoNilFields(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo) error: %v", err)
	}

	if pkt.ArchitectureRules == nil {
		t.Error("ArchitectureRules is nil; want empty (non-nil) slice")
	}
	if pkt.RuntimeEvidence == nil {
		t.Error("RuntimeEvidence is nil; want empty (non-nil) slice")
	}
}
