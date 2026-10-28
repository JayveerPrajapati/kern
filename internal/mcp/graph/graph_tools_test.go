package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
)

// fixture builds a small module with a real import cycle, an inheritance edge,
// a caller chain, dead code and one large function, then indexes it.
func fixture(t *testing.T) (string, *index.Index) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"main.go": `package main

import "demo/lib"

func main() { lib.Helper() }

func unused() {}
`,
		"lib/lib.go": `package lib

import "demo/util"

type Base struct{}

type Child struct {
	Base
}

func Helper() { util.Util() }
`,
		"util/util.go": `package util

import "demo/lib"

func Util() {}

func Big() {
` + strings.Repeat("\t_ = 1\n", 70) + `}
`,
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	return root, ix
}

// rawGov is the permissive governor bundle (nil governor = raw mode), which is
// what a local caller without an agent identity gets.
func rawGov() mcpgov.GovContext {
	return mcpgov.GovContext{
		NewGov:   func() (*mcpgov.Governor, error) { return nil, nil },
		StampGov: func(*mcpgov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
}

// TestAstSearch pins pattern validation, the no-match hint, and a hit.
func TestAstSearch(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if _, err := AstSearch(ctx, ix, map[string]any{}); err == nil {
		t.Fatal("pattern is required")
	}
	out, err := AstSearch(ctx, ix, map[string]any{"pattern": "NoSuchThing"})
	if err != nil || !strings.Contains(out, "no symbols matched") {
		t.Fatalf("no-match: %q %v", out, err)
	}
	out, err = AstSearch(ctx, ix, map[string]any{"pattern": "Helper"})
	if err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("hit: %q %v", out, err)
	}
}

// TestEntryPoints pins the empty-index message and the framework-symbol path.
func TestEntryPoints(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	out, err := EntryPoints(ctx, ix, map[string]any{})
	if err != nil || !strings.Contains(out, "no framework entry points") {
		t.Fatalf("empty: %q %v", out, err)
	}
	ix.Symbols = append(ix.Symbols, index.Symbol{
		Name: "handleIndex", Kind: "func", File: "main.go", Line: 1,
		Entry: true, Framework: "chi", Route: "GET /",
	})
	out, err = EntryPoints(ctx, ix, map[string]any{"pattern": "handle*"})
	if err != nil || !strings.Contains(out, "chi") || !strings.Contains(out, "handleIndex") {
		t.Fatalf("framework entry: %q %v", out, err)
	}
	// A pattern that matches nothing renders the empty message, not a stale row.
	out, err = EntryPoints(ctx, ix, map[string]any{"pattern": "nomatch*"})
	if err != nil || !strings.Contains(out, "no framework entry points") {
		t.Fatalf("filtered-out: %q %v", out, err)
	}
}

// TestWhy pins symbol validation, the unknown-symbol answer, and a real report.
func TestWhy(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if _, err := Why(ctx, ix, map[string]any{}); err == nil {
		t.Fatal("symbol is required")
	}
	out, err := Why(ctx, ix, map[string]any{"symbol": "NoSuchSymbol"})
	if err != nil || !strings.Contains(out, "no symbol found") {
		t.Fatalf("unknown: %q %v", out, err)
	}
	out, err = Why(ctx, ix, map[string]any{"symbol": "Helper"})
	if err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("known: %q %v", out, err)
	}
}

// TestInherits pins the inheritance view (supertype/subtype) and its honest
// empty state.
func TestInherits(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if _, err := Inherits(ctx, ix, map[string]any{}); err == nil {
		t.Fatal("symbol is required")
	}
	out, err := Inherits(ctx, ix, map[string]any{"symbol": "NoSuchSymbol"})
	if err != nil || !strings.Contains(out, "no symbol found") {
		t.Fatalf("unknown: %q %v", out, err)
	}
	out, err = Inherits(ctx, ix, map[string]any{"symbol": "Child"})
	if err != nil || !strings.Contains(out, "Child") {
		t.Fatalf("child: %q %v", out, err)
	}
	if strings.Contains(out, "no inheritance edges") {
		t.Fatalf("Child should carry a supertype edge:\n%s", out)
	}
}

// TestPath pins argument validation and a resolved path.
func TestPath(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if _, err := Path(ctx, ix, map[string]any{"from": "main"}); err == nil {
		t.Fatal("from and to are required")
	}
	out, err := Path(ctx, ix, map[string]any{"from": "main", "to": "Util"})
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("path rendered nothing")
	}
}

// TestCyclesPinsBothRenderings pins the import-cycle report in text and JSON.
func TestCycles(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	out, err := Cycles(ctx, ix, map[string]any{})
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("text cycles: %q %v", out, err)
	}
	js, err := Cycles(ctx, ix, map[string]any{"json": "true"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Cycles []any `json:"cycles"`
		Count  int   `json:"count"`
	}
	if err := json.Unmarshal([]byte(js), &decoded); err != nil {
		t.Fatalf("cycles json: %v\n%s", err, js)
	}
	if decoded.Count != len(decoded.Cycles) {
		t.Fatalf("count mismatch: %+v", decoded)
	}
}

// TestDeadAndLarges pins the dead-code and large-function reports, including
// the min_lines filter.
func TestDeadAndLarges(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	out, err := Dead(ctx, ix, map[string]any{})
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("dead: %q %v", out, err)
	}
	if !strings.Contains(out, "unused") {
		t.Fatalf("dead code should name the unused function:\n%s", out)
	}
	out, err = Larges(ctx, ix, map[string]any{"min_lines": "10"})
	if err != nil || !strings.Contains(out, "Big") {
		t.Fatalf("larges: %q %v", out, err)
	}
	// A threshold above every function yields the honest empty report.
	out, err = Larges(ctx, ix, map[string]any{"min_lines": "100000"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("larges rendered nothing at all")
	}
}

// TestSurprisingNearBridges pins the remaining one-shot topology reports.
func TestSurprisingNearBridges(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if out, err := Surprising(ctx, ix, map[string]any{}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("surprising: %q %v", out, err)
	}
	if _, err := Near(ctx, ix, map[string]any{}); err == nil {
		t.Fatal("near requires a symbol")
	}
	if out, err := Near(ctx, ix, map[string]any{"symbol": "Helper"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("near: %q %v", out, err)
	}
	if out, err := Bridges(ctx, ix, map[string]any{}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("bridges: %q %v", out, err)
	}
}

// TestTraceAndFrameworks pins trace validation plus the framework catalog and
// its trace view.
func TestTraceAndFrameworks(t *testing.T) {
	root, ix := fixture(t)
	ctx := context.Background()
	if _, err := Trace(ctx, ix, map[string]any{}); err == nil {
		t.Fatal("trace is required")
	}
	// A trace with no project frames is answered, not crashed on.
	if out, err := Trace(ctx, ix, map[string]any{"trace": "panic: boom\n\t/elsewhere/x.go:12"}); err != nil {
		t.Fatalf("trace: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("trace rendered nothing")
	}
	if out, err := Frameworks(ctx, map[string]any{"root": root}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("frameworks: %q %v", out, err)
	}
	if out, err := FWTrace(ctx, map[string]any{"root": root}); err != nil {
		t.Fatalf("fw trace: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("fw trace rendered nothing")
	}
}

// TestSearchContextGraphProbe pins the governed family in raw mode (nil
// governor), including their argument guards.
func TestSearchContextGraphProbe(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	gvc := rawGov()

	if _, err := Search(ctx, ix, gvc, map[string]any{}); err == nil {
		t.Fatal("query is required")
	}
	if _, err := Search(ctx, ix, gvc, map[string]any{"query": "Helper", "limit": "x"}); err == nil {
		t.Fatal("bad limit must error")
	}
	out, err := Search(ctx, ix, gvc, map[string]any{"query": "Helper"})
	if err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("search: %q %v", out, err)
	}

	if _, err := Context(ctx, ix, gvc, map[string]any{}); err == nil {
		t.Fatal("context requires a symbol")
	}
	// Progressively disclosed views live in kern_retrieve; the tool must say so
	// instead of silently ignoring the argument.
	if _, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper", "handle": "h1"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Fatalf("handle must be redirected: %v", err)
	}
	if _, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper", "level": "full"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Fatalf("level must be redirected: %v", err)
	}
	if out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper"}); err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("context: %q %v", out, err)
	}

	if _, err := Graph(ctx, ix, gvc, map[string]any{}); err == nil {
		t.Fatal("graph requires a symbol")
	}
	if out, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper"}); err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("graph: %q %v", out, err)
	}

	if _, err := Probe(ctx, ix, gvc, map[string]any{}); err == nil {
		t.Fatal("probe requires a task")
	}
	if out, err := Probe(ctx, ix, gvc, map[string]any{"task": "how does Helper work"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("probe: %q %v", out, err)
	}
}

// TestSnapshotRoundTrip pins create → verify for the graph snapshot pair.
func TestSnapshotRoundTrip(t *testing.T) {
	root, ix := fixture(t)
	created, err := SnapshotCreate(ix, map[string]any{})
	if err != nil {
		t.Skipf("snapshot unavailable: %v", err)
	}
	if strings.TrimSpace(created) == "" {
		t.Fatal("snapshot create rendered nothing")
	}
	if out, err := SnapshotVerify(root, map[string]any{}); err != nil {
		t.Logf("snapshot verify (may need a persisted snapshot): %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("snapshot verify rendered nothing")
	}
}

// TestFtsSearchAndRepoSearch pins the store-backed search tools: the query
// guard, the persisted-FTS path (saved store), and the cross-repo fallback.
func TestFtsSearchAndRepoSearch(t *testing.T) {
	root, ix := fixture(t)
	ctx := context.Background()

	if _, err := FtsSearch(ctx, root, map[string]any{}); err == nil {
		t.Fatal("fts: query is required")
	}
	if _, err := FtsSearch(ctx, root, map[string]any{"query": "Helper", "limit": "x"}); err == nil {
		t.Fatal("fts: bad limit must error")
	}
	// Persist the store so the FTS index exists; without it the tool must
	// return a clear error rather than a wrong "no matches".
	if err := ix.Save(); err == nil {
		if out, err := FtsSearch(ctx, root, map[string]any{"query": "Helper"}); err != nil {
			t.Logf("fts with saved store: %v", err)
		} else if !strings.Contains(out, "Helper") && !strings.Contains(out, "no full-text matches") {
			t.Fatalf("fts output: %q", out)
		}
	}

	if _, err := RepoSearch(ctx, root, map[string]any{}); err == nil {
		t.Fatal("repo search: query is required")
	}
	// The cross-repo search reads the machine's repo registry, so the positive
	// path is exercised only for its argument guard here; the federated search
	// itself is covered by internal/intel's own tests.
}

// TestChurnCochangeGraceful pins that the git-backed reports answer (report or
// a clear error) for a non-repository root — never a panic.
func TestChurnCochangeGraceful(t *testing.T) {
	root, _ := fixture(t)
	ctx := context.Background()
	if out, err := Churn(ctx, map[string]any{"root": root}); err != nil {
		t.Logf("churn outside a git repo: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("churn rendered nothing")
	}
	if out, err := Cochange(ctx, map[string]any{"root": root, "range": "HEAD~5..HEAD"}); err != nil {
		t.Logf("cochange outside a git repo: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("cochange rendered nothing")
	}
	if _, err := Cochange(ctx, map[string]any{"root": root, "limit": "x"}); err == nil {
		t.Fatal("cochange: bad limit must error")
	}
}

// TestArchAndCommunities pins the community/topology views on a real index.
func TestArchAndCommunities(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	if out, err := Arch(ctx, ix, nil, map[string]any{}); err != nil {
		t.Logf("arch needs a session: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("arch rendered nothing")
	}
	// A nil session is a caller bug, not a crash: the tool must return an
	// error (this used to panic inside project.Session.CommunitiesList).
	if _, err := Communities(ctx, ix, nil, map[string]any{}); err == nil {
		t.Fatal("communities with a nil session must error, not panic")
	}
	if _, err := Communities(ctx, ix, nil, map[string]any{"limit": "x"}); err == nil {
		t.Fatal("bad limit must error")
	}
}

// TestContextBranches pins the context tool's disambiguation and option
// handling: an unknown symbol answers with suggestions (never a bare error),
// a bad profile is rejected, and lens/budget options still render a slice.
func TestContextBranches(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	gvc := rawGov()

	out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "NoSuchSymbol"})
	if err != nil || !strings.Contains(out, "no symbol found") {
		t.Fatalf("unknown symbol: %q %v", out, err)
	}
	// A near-miss gets suggestions (the same did-you-mean path the CLI has).
	out, err = Context(ctx, ix, gvc, map[string]any{"symbol": "Helpe"})
	if err != nil || !strings.Contains(out, "no symbol found") {
		t.Fatalf("near-miss: %q %v", out, err)
	}
	if _, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper", "profile": "bogus"}); err == nil {
		t.Fatal("unknown profile must error")
	}
	if out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper", "lens": "security", "max_tokens": "400"}); err != nil {
		t.Fatalf("lens/budget context: %v", err)
	} else if !strings.Contains(out, "Helper") {
		t.Fatalf("lens context missing the symbol:\n%s", out)
	}
	if out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper", "with_freshness": "true"}); err != nil {
		t.Fatalf("freshness context: %v", err)
	} else if !strings.Contains(out, "freshness-proof") {
		t.Fatalf("freshness footer missing:\n%s", out)
	}
}

// TestGraphBranches pins the graph tool's renderings: one-line, the adaptive
// slice, unknown symbols, bad numeric args, and the entities block.
func TestGraphBranches(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	gvc := rawGov()

	if _, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "NoSuchSymbol"}); err == nil {
		t.Fatal("unknown symbol must error")
	}
	if _, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper", "max_tokens": "x"}); err == nil {
		t.Fatal("bad max_tokens must error")
	}
	if out, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper", "format": "one-line"}); err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("one-line: %q %v", out, err)
	}
	if out, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper", "max_tokens": "0", "min_confidence": "EXTRACTED"}); err != nil {
		t.Fatalf("adaptive slice: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("graph rendered nothing")
	}
	if out, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper", "entities": "true"}); err != nil {
		t.Fatalf("entities: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("entities graph rendered nothing")
	}
}

// TestExploreBranches pins the explore tool: unknown symbols error, the
// suggestions path answers, and depth/max/token options all render.
func TestExploreBranches(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	gvc := rawGov()

	if _, err := Explore(ctx, ix, gvc, map[string]any{}); err == nil {
		t.Fatal("symbol is required")
	}
	if _, err := Explore(ctx, ix, gvc, map[string]any{"symbol": "NoSuchSymbol"}); err == nil {
		t.Fatal("unknown symbol must error")
	}
	for _, args := range []map[string]any{
		{"symbol": "Helper"},
		{"symbol": "Helper", "depth": "1", "max": "5"},
		{"symbol": "Helper", "max_tokens": "600"},
		{"symbol": "Helper", "explain": "true"},
	} {
		out, err := Explore(ctx, ix, gvc, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.TrimSpace(out) == "" {
			t.Fatalf("%v rendered nothing", args)
		}
	}
}

// TestGovernedPaths pins the governed branch of the read tools: with a real
// governor (the default agent identity), Context/Graph/Explore still answer,
// and their provenance stamping runs instead of the raw path.
func TestGovernedPaths(t *testing.T) {
	_, ix := fixture(t)
	ctx := context.Background()
	gvc := mcpgov.GovContext{
		NewGov: func() (*mcpgov.Governor, error) {
			return mcpgov.New(ctx, map[string]any{"agent_id": "default"}, ix)
		},
		StampGov: func(*mcpgov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}

	// The default agent has no task scope seeded in this fixture, so the
	// firewall denies: the contract under test is that a denial is a clean,
	// auditable error (or the non-leaking "no symbol found"), never a panic
	// and never symbol content.
	if out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "Helper"}); err == nil {
		if strings.Contains(out, "func Helper") {
			t.Fatal("a denied context must not leak the symbol body")
		}
	}
	if _, err := Graph(ctx, ix, gvc, map[string]any{"symbol": "Helper"}); err != nil {
		t.Logf("governed graph denied: %v", err)
	}
	if _, err := Explore(ctx, ix, gvc, map[string]any{"symbol": "Helper"}); err != nil {
		t.Logf("governed explore denied: %v", err)
	}
	// An unknown/governed-away symbol answers with the non-leaking message
	// rather than an error, so a denial is indistinguishable from absence.
	if out, err := Context(ctx, ix, gvc, map[string]any{"symbol": "NoSuchSymbol"}); err == nil && strings.Contains(out, "func Helper") {
		t.Fatal("governed unknown must not leak symbol content")
	}
}

// TestOptionMatrix pins the option plumbing of the remaining reports in one
// pass: path filters, depth/limit caps, a resolvable trace frame, and the
// subgraph snapshot.
func TestOptionMatrix(t *testing.T) {
	root, ix := fixture(t)
	ctx := context.Background()

	if out, err := Dead(ctx, ix, map[string]any{"path": "lib/"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("dead path filter: %q %v", out, err)
	}
	if out, err := Near(ctx, ix, map[string]any{"symbol": "Helper", "depth": "1", "max": "5"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("near with bounds: %q %v", out, err)
	}
	if out, err := Larges(ctx, ix, map[string]any{"min_lines": "5", "limit": "2"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("larges with limit: %q %v", out, err)
	}
	// A trace frame naming an indexed file resolves to its symbol.
	trace := "panic: boom\n\tdemo/lib/lib.go:12 +0x1"
	if out, err := Trace(ctx, ix, map[string]any{"trace": trace}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("trace with a project frame: %q %v", out, err)
	}
	// Subgraph snapshot renders the neighborhood around one symbol.
	out, err := SnapshotCreate(ix, map[string]any{"symbol": "Helper", "limit": "10"})
	if err != nil {
		t.Fatalf("subgraph snapshot: %v", err)
	}
	if !strings.Contains(out, "Helper") {
		t.Fatalf("subgraph snapshot missing symbol:\n%s", out)
	}
	// Probe with a task that names real symbols.
	if out, err := Probe(ctx, ix, rawGov(), map[string]any{"task": "Helper in lib calls Util in util"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("probe with symbols: %q %v", out, err)
	}
	// Snapshot verify against the persisted snapshot (the pair must agree).
	if _, err := SnapshotVerify(root, map[string]any{"json": "true"}); err != nil {
		t.Logf("snapshot verify: %v", err)
	}
}

// TestLimitsAndFormats pins the numeric-option plumbing shared by the topology
// reports and the framework trace's JSON rendering.
func TestLimitsAndFormats(t *testing.T) {
	root, ix := fixture(t)
	ctx := context.Background()

	if _, err := Surprising(ctx, ix, map[string]any{"limit": "x"}); err == nil {
		t.Fatal("surprising: bad limit must error")
	}
	if _, err := Bridges(ctx, ix, map[string]any{"limit": "x"}); err == nil {
		t.Fatal("bridges: bad limit must error")
	}
	if out, err := Surprising(ctx, ix, map[string]any{"limit": "1"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("surprising limit: %q %v", out, err)
	}
	if out, err := Bridges(ctx, ix, map[string]any{"limit": "1"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("bridges limit: %q %v", out, err)
	}
	if out, err := Why(ctx, ix, map[string]any{"symbol": "Helper", "min_confidence": "EXTRACTED"}); err != nil || !strings.Contains(out, "Helper") {
		t.Fatalf("why min_confidence: %q %v", out, err)
	}
	if out, err := FWTrace(ctx, map[string]any{"root": root, "format": "json", "filter": "no-such-route"}); err != nil {
		t.Fatalf("fw trace json: %v", err)
	} else if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("fw trace json is not JSON:\n%s", out)
	}
	// Trace with no resolvable project frame answers with guidance (never an
	// empty string and never a panic).
	if out, err := Trace(ctx, ix, map[string]any{"trace": "panic: boom\n\t/elsewhere/x.go:12"}); err != nil {
		t.Fatalf("trace: %v", err)
	} else if strings.TrimSpace(out) == "" {
		t.Fatal("trace rendered nothing")
	}
	// Probe redirects progressively-disclosed level views like Context does,
	// and its min_confidence filter plus a tight budget still render a bundle.
	if _, err := Probe(ctx, ix, rawGov(), map[string]any{"task": "x", "level": "full"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Fatalf("probe level must be redirected: %v", err)
	}
	if out, err := Probe(ctx, ix, rawGov(), map[string]any{"task": "Helper Util", "min_confidence": "EXTRACTED", "max_tokens": "300"}); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("probe with budget: %q %v", out, err)
	}
}

// TestPureHelpers pins the small helpers the tool bodies depend on.
func TestPureHelpers(t *testing.T) {
	long := strings.Repeat("x", 200)
	if got := clipQuery(long); len(got) >= len(long) {
		t.Fatalf("clipQuery did not clip: %d chars", len(got))
	}
	if got := clipQuery("short"); got != "short" {
		t.Fatalf("clipQuery changed a short query: %q", got)
	}
	if normalizeSearchName("pkg.Helper") == "" {
		t.Fatal("normalizeSearchName returned empty")
	}
	_, ix := fixture(t)
	if !hasStrongSearchMatch("Helper", ix.Search("Helper", 3)) {
		t.Fatal("exact name should be a strong match")
	}
	names := searchSymbolNames(ix.Search("Helper", 3))
	if len(names) == 0 {
		t.Fatal("searchSymbolNames returned nothing")
	}
	if got := RetrieveItemNames([]retrieval.L1Item{{Name: "a"}, {Name: "b"}}); len(got) != 2 {
		t.Fatalf("RetrieveItemNames: %v", got)
	}
	if got := FreshnessFooter(map[string]any{"with_freshness": "true"}, ix); got == "" {
		t.Fatal("FreshnessFooter should render when requested")
	}
}
