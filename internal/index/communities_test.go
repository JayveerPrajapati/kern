package index

import (
	"testing"
)

func TestCommunityLabelsSmallRepo(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	labels := ix.CommunityLabels()
	if len(labels) == 0 {
		t.Fatalf("expected non-empty labels for a small repo, got none")
	}
}

func TestCommunityLabelsGatedLargeRepo(t *testing.T) {
	ix := &Index{}
	for i := 0; i < MaxCommunitySymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	if labels := ix.CommunityLabels(); len(labels) != 0 {
		t.Fatalf("expected empty labels above the symbol gate, got %d", len(labels))
	}
}
