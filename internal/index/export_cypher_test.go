package index

import (
	"strings"
	"testing"
)

// TestGraphCypherDeterministic covers the P2-4 Cypher/Neo4j file export:
// sorted deterministic node/edge statements, confidence tiers from the
// GraphML high/medium/low mapping, quote escaping, and byte-identical output
// across calls. The fixture deliberately lists nodes/edges unsorted and
// includes a double-quote in a node name plus duplicate (source, target)
// pairs at different tiers.
func TestGraphCypherDeterministic(t *testing.T) {
	g := GraphResult{
		Root: "pkg.FuncA",
		Nodes: []GraphNode{
			{ID: "pkg.FuncB", Name: "pkg.FuncB", Kind: "func", File: "b.go", Line: 21},
			{ID: `pkg.Quoted"Name`, Name: `pkg.Quoted"Name`, Kind: "func", File: "q.go", Line: 7},
			{ID: "pkg.FuncA", Name: "pkg.FuncA", Kind: "func", File: "a.go", Line: 3},
		},
		Edges: []GraphEdge{
			{From: "pkg.FuncA", To: "pkg.FuncB", Confidence: confHigh},
			{From: "pkg.FuncB", To: `pkg.Quoted"Name`, Confidence: confLow},
			{From: "pkg.FuncA", To: "pkg.FuncB", Confidence: confMedium},
			{From: "pkg.FuncB", To: "pkg.FuncA", Confidence: confLow},
		},
	}

	out := g.GraphCypher()

	// (e) Determinism: two calls are byte-identical.
	if again := g.GraphCypher(); again != out {
		t.Fatalf("GraphCypher must be byte-identical across calls:\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}

	// Header comment line present (file interop only, no server push).
	if !strings.HasPrefix(out, "// Cypher export for Neo4j interoperability (file interop only — no server push).\n") {
		t.Errorf("missing/incorrect header comment line, got:\n%s", out)
	}

	// (a) One CREATE per node, all four properties emitted, sorted by fullname.
	if got := strings.Count(out, "CREATE (:Symbol"); got != 3 {
		t.Errorf("expected 3 node CREATE statements, got %d:\n%s", got, out)
	}
	full := `CREATE (:Symbol {name: "pkg.FuncA", kind: "func", file: "a.go", line: 3});`
	if !strings.Contains(out, full) {
		t.Errorf("node CREATE must emit all four properties:\n%s", out)
	}
	fa := strings.Index(out, `CREATE (:Symbol {name: "pkg.FuncA"`)
	fb := strings.Index(out, `CREATE (:Symbol {name: "pkg.FuncB"`)
	fq := strings.Index(out, `CREATE (:Symbol {name: "pkg.Quoted\"Name"`)
	if fa < 0 || fb < 0 || fq < 0 {
		t.Fatalf("missing node CREATE statement in:\n%s", out)
	}
	if !(fa < fb && fb < fq) {
		t.Errorf("node CREATE statements not sorted by fullname asc (FuncA=%d FuncB=%d Quoted=%d)", fa, fb, fq)
	}

	// (b) One MATCH/CREATE per edge, sorted by (source, target) asc.
	if got := strings.Count(out, "MATCH (a:Symbol"); got != 4 {
		t.Errorf("expected 4 edge MATCH/CREATE statements, got %d:\n%s", got, out)
	}
	ab := strings.Index(out, `MATCH (a:Symbol {name: "pkg.FuncA"}), (b:Symbol {name: "pkg.FuncB"}) CREATE (a)-[:CALLS`)
	ba := strings.Index(out, `MATCH (a:Symbol {name: "pkg.FuncB"}), (b:Symbol {name: "pkg.FuncA"}) CREATE (a)-[:CALLS`)
	bq := strings.Index(out, `MATCH (a:Symbol {name: "pkg.FuncB"}), (b:Symbol {name: "pkg.Quoted\"Name"}) CREATE (a)-[:CALLS`)
	if ab < 0 || ba < 0 || bq < 0 {
		t.Fatalf("missing edge MATCH/CREATE statement in:\n%s", out)
	}
	// Expected order after (From,To) sort: (A,B)x2, (B,A), (B,Q).
	if !(ab < ba && ba < bq) {
		t.Errorf("edge statements not sorted by (source, target): AB=%d BA=%d BQ=%d", ab, ba, bq)
	}

	// (c) Confidence tier strings (GraphML high/medium/low mapping) present.
	for _, tier := range []string{`confidence: "high"`, `confidence: "medium"`, `confidence: "low"`} {
		if !strings.Contains(out, tier) {
			t.Errorf("missing tier %s in:\n%s", tier, out)
		}
	}

	// (d) A node name containing a double quote is escaped as \".
	if !strings.Contains(out, `name: "pkg.Quoted\"Name"`) {
		t.Errorf("double quote in node name not escaped:\n%s", out)
	}
}

// TestGraphCypherBackslashEscaping ensures a backslash in a property value is
// escaped for the Cypher string literal.
func TestGraphCypherBackslashEscaping(t *testing.T) {
	g := GraphResult{
		Nodes: []GraphNode{{ID: `pkg.A\B`, Name: `pkg.A\B`, Kind: "func", File: `dir\a.go`, Line: 1}},
	}
	out := g.GraphCypher()
	if !strings.Contains(out, `name: "pkg.A\\B"`) {
		t.Errorf("backslash in node name not escaped:\n%s", out)
	}
	if !strings.Contains(out, `file: "dir\\a.go"`) {
		t.Errorf("backslash in file property not escaped:\n%s", out)
	}
}
