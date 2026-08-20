package edges

import (
	"testing"
)

func TestChangedByEdges(t *testing.T) {
	commitFiles := map[string][]string{
		"abc123": {"main.go", "utils.go"},
	}
	edges := ChangedByEdges(commitFiles)
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(edges))
	}
	if edges[0].Kind != KindChangedBy {
		t.Error("wrong kind")
	}
	if edges[0].To != "commit:abc123" {
		t.Error("wrong target")
	}
}

func TestAffectsEdges(t *testing.T) {
	edges := AffectsEdges("FuncA", []string{"FuncB", "FuncC", "FuncD"})
	if len(edges) != 3 {
		t.Fatalf("edges = %d, want 3", len(edges))
	}
	for _, e := range edges {
		if e.From != "FuncA" || e.Kind != KindAffects {
			t.Errorf("bad edge: %+v", e)
		}
	}
}

func TestCausedEdges(t *testing.T) {
	incidents := map[string]string{
		"INC-1": "CommitA",
		"INC-2": "FuncB",
	}
	edges := CausedEdges(incidents)
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(edges))
	}
}

func TestFixedByEdges(t *testing.T) {
	fixes := map[string]string{
		"INC-1": "0123456789abcdef0123456789abcdef01234567", // 40-char SHA
		"INC-2": "42",                                       // PR number
	}
	edges := FixedByEdges(fixes)
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(edges))
	}
	if edges[0].To != "commit:0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("SHA edge: %v", edges[0])
	}
	if edges[1].To != "pr:42" {
		t.Errorf("PR edge: %v", edges[1])
	}
}

func TestViolatesEdges(t *testing.T) {
	violations := map[string]string{
		"file:src/main.go": "no-business-logic-in-handlers",
	}
	edges := ViolatesEdges(violations)
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if edges[0].To != "rule:no-business-logic-in-handlers" {
		t.Error("wrong rule target")
	}
}

func TestOwnsEdges(t *testing.T) {
	ownership := map[string]string{
		"file:src/main.go": "@backend-team",
	}
	edges := OwnsEdges(ownership)
	if len(edges) != 1 || edges[0].From != "@backend-team" || edges[0].Kind != KindOwns {
		t.Errorf("bad owns edge: %+v", edges[0])
	}
}

func TestRelatedToEdgesBidirectional(t *testing.T) {
	pairs := [][2]string{{"FuncA", "FuncB"}}
	edges := RelatedToEdges(pairs)
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2 (bidirectional)", len(edges))
	}
	// Should have both A→B and B→A
	hasAB, hasBA := false, false
	for _, e := range edges {
		if e.From == "FuncA" && e.To == "FuncB" {
			hasAB = true
		}
		if e.From == "FuncB" && e.To == "FuncA" {
			hasBA = true
		}
	}
	if !hasAB || !hasBA {
		t.Error("missing bidirectional edges")
	}
}
