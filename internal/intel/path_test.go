package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestShortestPathPrefersExtractedOverInferredShortcut(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app/app.go": `package app

func Entry() {
	Middle()
}

func Middle() {
	Target()
}

func Target() {}
`,
	})
	ix := buildIndex(t, dir)

	// Add an artificial INFERRED shortcut edge from Entry to Target
	// (mirroring virtual dispatch shortcuts)
	ix.Calls["Entry"] = append(ix.Calls["Entry"], index.CallEdge{Target: "Target", Confidence: index.ConfidenceLow})

	path := ShortestPath(ix, "Entry", "Target")
	if len(path) != 3 {
		t.Fatalf("expected 3-hop path [Entry Middle Target], got %v", path)
	}
	if path[0] != "Entry" || path[1] != "Middle" || path[2] != "Target" {
		t.Errorf("unexpected path: %v", path)
	}

	rendered := RenderPath(ix, path)
	if !strings.Contains(rendered, "Middle") {
		t.Errorf("expected rendered path to include intermediate hop Middle, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[EXTRACTED]") {
		t.Errorf("expected rendered path to have [EXTRACTED] tags, got:\n%s", rendered)
	}
}
