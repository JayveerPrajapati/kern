package intel

import (
	"strings"
	"testing"
)

// TestRenderPathMarksBackwardHops pins that a path rendered in the reverse
// direction does not print forward arrows: the search follows call edges in
// either direction, so the renderer must annotate each hop's real direction.
func TestRenderPathMarksBackwardHops(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)

	forward := ShortestPath(ix, "Caller", "Public")
	if len(forward) == 0 {
		t.Fatal("forward path Caller -> Public not found")
	}
	out := RenderPath(ix, forward)
	if !strings.Contains(out, "\u2192") || strings.Contains(out, "\u2190") {
		t.Errorf("forward path should use only \u2192:\n%s", out)
	}

	reverse := ShortestPath(ix, "Public", "Caller")
	if len(reverse) == 0 {
		t.Fatal("reverse path Public -> Caller not found")
	}
	out = RenderPath(ix, reverse)
	if !strings.Contains(out, "\u2190") {
		t.Errorf("reverse path should mark the backward hop with \u2190:\n%s", out)
	}
	if !strings.Contains(out, "caller\u2192callee") {
		t.Errorf("reverse path should include the direction legend:\n%s", out)
	}
}
