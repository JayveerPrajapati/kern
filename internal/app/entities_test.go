package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// entityFixture writes a tiny Go module with one HTTP route registration so
// the twin API extractor emits an "api" entity node wired to its handler
// (handleUsers), plus an isolated symbol (unrelated) in its own file with no
// twin connections. Returns the fixture root.
func entityFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module entityfix\n\ngo 1.20\n",
		"main.go": `package main

import "net/http"

// handleUsers serves the /users endpoint.
func handleUsers(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/users", handleUsers)
	_ = http.DefaultServeMux
}
`,
		"other.go": `package main

// unrelated has no twin connections (own file, no route registration).
func unrelated() string { return "x" }
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

// TestImpactPopulatesEntities is the integration case: TaskService.Impact over
// a fixture whose twin graph wires an api entity to the target must populate
// report.Entities and render the "Affected entities" section.
func TestImpactPopulatesEntities(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	root := entityFixture(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	_, rep, text, err := ts.Impact("handleUsers")
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if len(rep.Entities) == 0 {
		t.Fatal("report.Entities empty — expected the /users api entity")
	}
	e := rep.Entities[0]
	if e.Kind != "api" || e.Name != "GET /users" {
		t.Fatalf("entity = (%s, %q), want (api, \"GET /users\")", e.Kind, e.Name)
	}
	if e.File != "main.go" {
		t.Fatalf("entity file = %q, want main.go", e.File)
	}
	if len(e.Symbols) == 0 || e.Symbols[0] != "handleUsers" {
		t.Fatalf("entity symbols = %v, want [handleUsers]", e.Symbols)
	}
	if !strings.Contains(text, "Affected entities: 1") {
		t.Fatalf("impact text missing entities section:\n%s", text)
	}
	if !strings.Contains(text, "- api GET /users (main.go)  ← handleUsers") {
		t.Fatalf("impact text missing entity row:\n%s", text)
	}
	// JSON must carry the additive field.
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(b), `"Entities"`) {
		t.Fatalf("impact JSON missing Entities field: %s", b)
	}
}

// TestWhatIfPopulatesEntities is the what-if path integration case: the twin
// entity implicated by the change target surfaces on the what-if Impact.
func TestWhatIfPopulatesEntities(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	root := entityFixture(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	imp, text, err := p.WhatIf(whatif.RemoveSymbol, "handleUsers", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if len(imp.Entities) == 0 {
		t.Fatal("impact.Entities empty — expected the /users api entity")
	}
	e := imp.Entities[0]
	if e.Kind != "api" || e.Name != "GET /users" || e.File != "main.go" {
		t.Fatalf("entity = (%s, %q, %q), want (api, \"GET /users\", main.go)", e.Kind, e.Name, e.File)
	}
	if !strings.Contains(text, "Affected entities: 1") {
		t.Fatalf("what-if text missing entities section:\n%s", text)
	}
	if !strings.Contains(text, "- api GET /users (main.go)  ← handleUsers") {
		t.Fatalf("what-if text missing entity row:\n%s", text)
	}
}

// TestImpactNoEntitiesOmitsSection is the no-entity case: a symbol with no
// twin connections leaves Entities empty and the text render omits the
// section entirely (zero-cost, no behavior change).
func TestImpactNoEntitiesOmitsSection(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	root := entityFixture(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	_, rep, text, err := ts.Impact("unrelated")
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if len(rep.Entities) != 0 {
		t.Fatalf("report.Entities = %v, want empty for an entity-free symbol", rep.Entities)
	}
	if strings.Contains(text, "Affected entities") {
		t.Fatalf("text must omit the entities section when none are implicated:\n%s", text)
	}
	// The pure what-if render behaves the same way.
	plain := renderWhatIfText(whatif.RemoveSymbol, "unrelated", "unrelated", whatif.Impact{})
	if strings.Contains(plain, "Affected entities") {
		t.Fatalf("what-if text must omit the entities section when none are implicated:\n%s", plain)
	}
}

// TestEntityAggregationDedupesAndSorts is the deterministic-ordering unit
// case: multiple symbols implicating shared entities merge and dedupe by
// (Kind, Name, File), and the result sorts by (Kind, Name, File) with sorted
// symbol lists.
func TestEntityAggregationDedupesAndSorts(t *testing.T) {
	g := &intel.Graph{Graph: domain.Graph{
		Nodes: []domain.Node{
			{ID: "api.HandlerA", Kind: "symbol", Label: "HandlerA", Symbol: &domain.Symbol{Name: "HandlerA", Qualified: "api.HandlerA", File: "api/a.go"}},
			{ID: "api.HandlerB", Kind: "symbol", Label: "HandlerB", Symbol: &domain.Symbol{Name: "HandlerB", Qualified: "api.HandlerB", File: "api/b.go"}},
			{ID: "api:gin:GET:/users", Kind: "api", Label: "GET /users", API: &domain.API{Name: "GET /users", File: "api/routes.go"}},
			{ID: "api:gin:GET:/health", Kind: "api", Label: "GET /health", API: &domain.API{Name: "GET /health", File: "api/health.go"}},
			{ID: "table:app:users", Kind: "table", Label: "users", Table: &domain.Table{Name: "users", Database: "app"}},
		},
		Edges: []domain.Edge{
			{From: "api:gin:GET:/users", To: "HandlerA", Kind: "implements"},
			{From: "api:gin:GET:/users", To: "HandlerB", Kind: "implements"},
			{From: "api:gin:GET:/health", To: "HandlerB", Kind: "implements"},
			{From: "table:app:users", To: "file:api/a.go", Kind: "defined_in"},
		},
	}}
	ents := attachImpactEntities(g, []string{"api.HandlerB", "api.HandlerA", "api.HandlerB"})
	if len(ents) != 3 {
		t.Fatalf("got %d entities, want 3 (dedupe by Kind+Name+File): %+v", len(ents), ents)
	}
	// Sorted by (Kind, Name, File): api GET /health, api GET /users, table users.
	if ents[0].Name != "GET /health" || ents[1].Name != "GET /users" || ents[2].Kind != "table" {
		t.Fatalf("entity order = %+v, want health, users, table", ents)
	}
	if ents[1].Name == "GET /users" {
		// Both handlers implicate /users — merged and sorted, deduped.
		if len(ents[1].Symbols) != 2 || ents[1].Symbols[0] != "api.HandlerA" || ents[1].Symbols[1] != "api.HandlerB" {
			t.Fatalf("/users symbols = %v, want [api.HandlerA api.HandlerB]", ents[1].Symbols)
		}
	}
	if ents[2].File != "api/a.go" {
		t.Fatalf("table file = %q, want api/a.go (from defined_in edge)", ents[2].File)
	}
	// The what-if variant mirrors the same shape.
	we := attachWhatIfEntities(g, []string{"api.HandlerA"})
	if len(we) != 2 || we[0].Kind != "api" || we[1].Kind != "table" {
		t.Fatalf("what-if entities = %+v, want api + table", we)
	}
}
