package intel

// Slice 5: the persisted-graph fast path (NewGraphFromSnapshot) must carry
// exactly the construction state of a fresh FromIndex build. These tests pin
// node count, edge count, and the node/edge multisets for a stable fixture,
// and persist the counts in a golden file so a future format change is caught.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func goldenGraphFixture() map[string]string {
	return map[string]string{
		"go.mod":           "module fx\n\ngo 1.20\n",
		"shared/shared.go": "package shared\n\nfunc Shared() int { return 1 }\n\nfunc (Thing) Do() int { return Shared() }\n\ntype Thing struct{}\n",
		"a/a.go":           "package a\n\nimport \"fx/shared\"\n\nfunc Alpha() int { return shared.Shared() }\n",
	}
}

func nodeSignature(n domain.Node) string {
	parts := []string{n.ID, n.Kind, n.Label, n.Package, n.Resolution}
	candidates := append([]string(nil), n.Candidates...)
	sort.Strings(candidates)
	parts = append(parts, candidates...)
	return strings.Join(parts, "|")
}

func edgeSignature(e domain.Edge) string {
	return strings.Join([]string{
		e.From, e.To, e.Kind, e.File, fmt.Sprintf("%d", e.Line),
		e.Reference, e.Resolution, e.Confidence, e.Synth,
	}, "|")
}

func TestFromIndexEqualsSnapshotRoundTrip(t *testing.T) {
	root := writeTree(t, goldenGraphFixture())
	ix := buildIndex(t, root)

	fresh := FromIndex(ix)
	round := NewGraphFromSnapshot(fresh.Snapshot())

	if len(fresh.Nodes) != len(round.Nodes) {
		t.Fatalf("node count: fresh=%d snapshot=%d", len(fresh.Nodes), len(round.Nodes))
	}
	if len(fresh.Edges) != len(round.Edges) {
		t.Fatalf("edge count: fresh=%d snapshot=%d", len(fresh.Edges), len(round.Edges))
	}
	if len(fresh.Nodes) == 0 || len(fresh.Edges) == 0 {
		t.Fatalf("fixture produced an empty graph (nodes=%d edges=%d)", len(fresh.Nodes), len(fresh.Edges))
	}

	freshNodes := make([]string, 0, len(fresh.Nodes))
	for _, n := range fresh.Nodes {
		freshNodes = append(freshNodes, nodeSignature(n))
	}
	roundNodes := make([]string, 0, len(round.Nodes))
	for _, n := range round.Nodes {
		roundNodes = append(roundNodes, nodeSignature(n))
	}
	sort.Strings(freshNodes)
	sort.Strings(roundNodes)
	if strings.Join(freshNodes, "\n") != strings.Join(roundNodes, "\n") {
		t.Fatal("node multiset drifted across the snapshot round-trip")
	}

	freshEdges := make([]string, 0, len(fresh.Edges))
	for _, e := range fresh.Edges {
		freshEdges = append(freshEdges, edgeSignature(e))
	}
	roundEdges := make([]string, 0, len(round.Edges))
	for _, e := range round.Edges {
		roundEdges = append(roundEdges, edgeSignature(e))
	}
	sort.Strings(freshEdges)
	sort.Strings(roundEdges)
	if strings.Join(freshEdges, "\n") != strings.Join(roundEdges, "\n") {
		t.Fatal("edge multiset drifted across the snapshot round-trip")
	}
}

// TestFromIndexGoldenCounts pins the counts for the stable fixture. Run with
// UPDATE_GOLDEN=1 to regenerate after a conscious format change.
func TestFromIndexGoldenCounts(t *testing.T) {
	root := writeTree(t, goldenGraphFixture())
	ix := buildIndex(t, root)
	g := FromIndex(ix)
	got := fmt.Sprintf("nodes=%d\nedges=%d\n", len(g.Nodes), len(g.Edges))
	golden := filepath.Join("testdata", "fromindex_counts.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (run UPDATE_GOLDEN=1 go test ./internal/intel -run TestFromIndexGoldenCounts): %v", golden, err)
	}
	if string(want) != got {
		t.Fatalf("FromIndex counts drifted:\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}
