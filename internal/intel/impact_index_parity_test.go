package intel

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// callsIndexFixture builds a small multi-package index that exercises the
// ambiguous-name, receiver-qualified, predeclared-builtin and foreign-callee
// branches of the callee/caller resolvers.
func callsIndexFixture(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a/a.go": `package a

import "fmt"

type Server struct{}

func (s *Server) Send() { Helper() }
func Helper()           {}
func New() *Server      { return &Server{} }
func Use()              { Helper(); append([]int{}, 1); fmt.Println("x") }
`,
		"b/b.go": `package b

import "example/a"

type Server struct{}

func (s *Server) Send() { Helper() }
func Helper()           {}
func UseA()             { a.Helper() }
`,
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	return ix
}

// refCallers is the pre-index DirectCallersNames (full edge scan through the
// frozen calleeIsTarget oracle).
func refCallers(g *Graph, symbol string, strict bool) []string {
	target := g.resolveSymbol(symbol)
	langByID := map[string]string{}
	if strict {
		for _, n := range g.Nodes {
			if n.Symbol != nil && n.Symbol.Language != "" {
				langByID[n.ID] = n.Symbol.Language
			}
		}
	}
	byID := g.nodesByID()
	seen := map[string]bool{}
	seenNames := map[string]bool{}
	var out []string
	for _, e := range g.Edges {
		if e.Kind != "calls" || !g.calleeIsTarget(e, target) {
			continue
		}
		from := e.From
		if fromID, ok := g.resolveCallerNodeID(e.From, target, false); ok {
			if strict {
				if g.precisionByLang[langByID[fromID]] != "resolved" {
					continue
				}
			}
			if fromID == target {
				continue
			}
			from = fromID
		} else if strict {
			continue
		}
		if seen[from] {
			continue
		}
		seen[from] = true
		name := from
		if n, ok := byID[from]; ok && n.Symbol != nil {
			if n.Symbol.Qualified != "" {
				name = n.Symbol.Qualified
			} else {
				name = n.Symbol.Name
			}
		} else if n, ok := byID[from]; ok && n.Label != "" {
			name = n.Label
		}
		if !seenNames[name] {
			seenNames[name] = true
			out = append(out, name)
		}
	}
	return out
}

// refDepends is the pre-index DirectDependsOnNames (full callerAttr scan).
func refDepends(g *Graph, symbol string, strict bool) []string {
	target := g.resolveSymbol(symbol)
	langByID := map[string]string{}
	if strict {
		for _, n := range g.Nodes {
			if n.Symbol != nil && n.Symbol.Language != "" {
				langByID[n.ID] = n.Symbol.Language
			}
		}
	}
	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []string
	for _, a := range g.callerIndex() {
		var fromID string
		switch {
		case a.resolvedOK:
			fromID = a.resolved
		case g.endpointNamesNode(a.from, target):
			fromID = target
		case a.anchoredOK:
			fromID = a.anchored
		default:
			continue
		}
		if fromID != target {
			continue
		}
		if strict {
			if g.precisionByLang[langByID[fromID]] != "resolved" {
				continue
			}
		}
		name := a.to
		if node, found := byID[a.to]; found && node.Kind == "reference" {
			name = node.Label
		}
		if !g.foreignCallee(a.to) {
			if toID, ok := g.ResolveEdgeEndpoint(a.to, a.from); ok {
				if n, ok2 := byID[toID]; ok2 && n.Symbol != nil {
					if n.Symbol.Qualified != "" {
						name = n.Symbol.Qualified
					} else {
						name = n.Symbol.Name
					}
				}
			}
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func parityGraphs(t *testing.T) map[string]*Graph {
	t.Helper()
	out := map[string]*Graph{}
	g1 := FromIndex(fakeIndex())
	out["fake"] = &g1
	g2 := FromIndex(callsIndexFixture(t))
	out["multi"] = &g2
	return out
}

// TestCalleeCandidatesMatchOracle sweeps every call edge against every node:
// the indexed candidate set must contain exactly the nodes the frozen
// calleeIsTarget oracle accepts. This pins the target-independent candidate
// extraction (the core of the impact consolidation) to the shipped semantics.
func TestCalleeCandidatesMatchOracle(t *testing.T) {
	for name, g := range parityGraphs(t) {
		for i, e := range g.Edges {
			if e.Kind != "calls" {
				continue
			}
			set := map[string]bool{}
			for _, id := range g.calleeCandidates(e) {
				set[id] = true
				if !g.calleeIsTarget(e, id) {
					t.Fatalf("%s edge %d %q->%q: candidate %q rejected by oracle", name, i, e.From, e.To, id)
				}
			}
			for id := range g.nodesByID() {
				if want := g.calleeIsTarget(e, id); want != set[id] {
					t.Fatalf("%s edge %d %q->%q: candidate(%q)=%v, oracle=%v", name, i, e.From, e.To, id, set[id], want)
				}
			}
			// The built index must file the edge under every candidate.
			for _, id := range g.calleeCandidates(e) {
				found := false
				for _, ei := range g.calleeIndex()[id] {
					if ei == i {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s edge %d: candidate %q missing from calleeIndex", name, i, id)
				}
			}
		}
	}
}

// TestImpactIndexQueryParity asserts the indexed DirectCallersNames and
// DirectDependsOnNames return byte-identical results to the pre-index scan
// for every node and both precision modes.
func TestImpactIndexQueryParity(t *testing.T) {
	for name, g := range parityGraphs(t) {
		for _, n := range g.Nodes {
			for _, strict := range []bool{false, true} {
				got := strings.Join(g.DirectCallersNames(n.ID, strict), "\x00")
				want := strings.Join(refCallers(g, n.ID, strict), "\x00")
				if got != want {
					t.Fatalf("%s callers(%s, strict=%v):\n got=%q\nwant=%q", name, n.ID, strict, got, want)
				}
				got = strings.Join(g.DirectDependsOnNames(n.ID, strict), "\x00")
				want = strings.Join(refDepends(g, n.ID, strict), "\x00")
				if got != want {
					t.Fatalf("%s depends(%s, strict=%v):\n got=%q\nwant=%q", name, n.ID, strict, got, want)
				}
			}
		}
	}
}

// TestServicesAffectedSorted pins the deterministic order the doc contract
// always claimed ("Distinct modules are returned, sorted"): the pre-index
// implementation iterated the node map, so the order varied run to run.
func TestServicesAffectedSorted(t *testing.T) {
	g := FromIndex(fakeIndex())
	svc := g.WhatServicesAffectedPrecise("HandleUsers", false)
	if len(svc) == 0 {
		t.Fatal("expected at least one affected service for the svc entry point")
	}
	if !sort.SliceIsSorted(svc, func(i, j int) bool { return svc[i].ID < svc[j].ID }) {
		t.Fatalf("services not sorted by ID: %v", func() []string {
			var ids []string
			for _, n := range svc {
				ids = append(ids, n.ID)
			}
			return ids
		}())
	}
}
