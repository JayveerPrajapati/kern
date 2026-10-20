package brief

import (
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestBuildNoMemoryOmitsMemorySections pins F2: kern_buddy's no_memory
// option must skip BOTH memory sections ("Project memory" and "Recent
// session activity") — a memory-free digest for subagent lanes that already
// carry session context. The default digest keeps both.
func TestBuildNoMemoryOmitsMemorySections(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := memory.AddExplicit(root, "always run tests before committing"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AddAuto(root, "Edited /tmp/lane/a.go"); err != nil {
		t.Fatal(err)
	}

	withMem, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withMem, "## Project memory") || !strings.Contains(withMem, "## Recent session activity") {
		t.Fatalf("default digest must include both memory sections:\n%s", withMem)
	}
	if !strings.Contains(withMem, "always run tests before committing") {
		t.Fatalf("explicit lesson missing from default digest:\n%s", withMem)
	}

	noMem, err := BuildWithOptions(root, Options{NoMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(noMem, "## Project memory") || strings.Contains(noMem, "## Recent session activity") {
		t.Fatalf("no_memory digest must omit both memory sections:\n%s", noMem)
	}
	if strings.Contains(noMem, "always run tests before committing") {
		t.Fatalf("no_memory digest must not render lessons:\n%s", noMem)
	}
	// The rest of the digest (index/architecture/how-to-use) must survive.
	if !strings.Contains(noMem, "## How to use kern in this session") {
		t.Fatalf("no_memory digest lost the cheatsheet:\n%s", noMem)
	}
}

// TestFrameworkEntriesDedupesByNameRoute pins F2: the same handler
// registered twice with the same (name, route) — the live "/v1/memory"
// duplicate, where Route was never copied on some index paths — collapses to
// one entry, while a distinct route on the same name survives.
func TestFrameworkEntriesDedupesByNameRoute(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Entry: true, Framework: "go", Route: "", Name: "MemoryHandler", File: "handlers.go", Line: 1},
			{Entry: true, Framework: "go", Route: "", Name: "MemoryHandler", File: "handlers.go", Line: 2},
			{Entry: true, Framework: "go", Route: "/v1/memory", Name: "MemoryHandler", File: "handlers.go", Line: 3},
		},
	}
	out, _ := frameworkEntries(ix)
	// (MemoryHandler, "") collapses to one; (MemoryHandler, "/v1/memory")
	// is a distinct key and survives.
	if len(out) != 2 {
		t.Fatalf("expected 2 entries after (name, route) dedupe, got %d: %+v", len(out), out)
	}
	routes := map[string]bool{}
	for _, e := range out {
		routes[e.route] = true
	}
	if !routes[""] || !routes["/v1/memory"] {
		t.Fatalf("routes = %v, want both \"\" and \"/v1/memory\"", routes)
	}
}

// TestFrameworkEntriesSkipsTestAndFixtureSymbols pins F2: entry-point
// symbols defined in test files or testdata fixtures are not part of the
// digest's HTTP surface.
func TestFrameworkEntriesSkipsTestAndFixtureSymbols(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Entry: true, Framework: "go", Route: "/v1/user", Name: "UserHandler", File: "handlers.go", Line: 1},
			{Entry: true, Framework: "go", Route: "/v1/user", Name: "UserHandler", File: "handlers_test.go", Line: 1},
			{Entry: true, Framework: "go", Route: "/v1/user", Name: "UserHandler", File: "testdata/fix.go", Line: 1},
		},
	}
	out, _ := frameworkEntries(ix)
	if len(out) != 1 {
		t.Fatalf("expected only the production handler, got %d: %+v", len(out), out)
	}
	if out[0].name != "UserHandler" || out[0].route != "/v1/user" {
		t.Fatalf("expected the production handler entry, got %+v", out[0])
	}
}

// TestFrameworkEntriesPrimaryLanguageFirst pins F3-1: on a Go-primary
// repo the dominant language's server routes (net-http) take the capped
// slots before JS client fetch wrappers (js-router) — the old plain
// fw-alphabetical sort ("js-router" < "net-http") filled every slot with
// the TS SDK's client routes and hid the Go surface entirely.
func TestFrameworkEntriesPrimaryLanguageFirst(t *testing.T) {
	var syms []index.Symbol
	// Go is dominant: 10 net-http entries plus plain non-entry Go symbols …
	for i := 0; i < 10; i++ {
		syms = append(syms, index.Symbol{Entry: true, Framework: "net-http", Lang: "go",
			Route: "/go" + strconv.Itoa(i), Name: "GoHandler" + strconv.Itoa(i), File: "srv.go", Line: i + 1})
	}
	for i := 0; i < 20; i++ {
		syms = append(syms, index.Symbol{Lang: "go", Name: "Plain" + strconv.Itoa(i), File: "plain.go", Line: i + 1})
	}
	// … and 15 js-router client entries (alphabetically first).
	for i := 0; i < 15; i++ {
		syms = append(syms, index.Symbol{Entry: true, Framework: "js-router", Lang: "typescript",
			Route: "/js" + strconv.Itoa(i), Name: "JsHandler" + strconv.Itoa(i), File: "client.ts", Line: i + 1})
	}
	out, total := frameworkEntries(&index.Index{Symbols: syms})
	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
	}
	if len(out) != 20 {
		t.Fatalf("expected capped at 20, got %d", len(out))
	}
	for i, e := range out {
		if i < 10 && e.fw != "net-http" {
			t.Fatalf("primary Go entries must rank first: slot %d = %+v", i, e)
		}
		if i >= 10 && e.fw != "js-router" {
			t.Fatalf("non-primary js-router entries must follow: slot %d = %+v", i, e)
		}
	}
}

// TestIndexSectionExcludesTestCallersOfProductionHub pins F2: a production
// symbol called ONLY from test files is not a hub — the digest's hub count
// must align with intel's production-caller exclusion (prodCallersWithFileMap
// via intel.Hubs), not raw ix.Callers.
func TestIndexSectionExcludesTestCallersOfProductionHub(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "prod", File: "a.go", Line: 1},
			{Kind: "func", Name: "TestProd", File: "a_test.go", Line: 1},
			{Kind: "func", Name: "TestOther", File: "a_test.go", Line: 1},
		},
		Callers:    map[string][]string{"prod": {"TestProd", "TestOther"}},
		FileHashes: map[string]string{"a.go": "h", "a_test.go": "h"},
	}
	out := indexSection(ix)
	if strings.Contains(out, "prod") {
		t.Fatalf("production symbol called only from tests must not rank as a hub, got %q", out)
	}
}
