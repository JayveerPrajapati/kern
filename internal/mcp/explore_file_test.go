package mcp

import (
	"strings"
	"testing"
)

func TestExploreFileSummary(t *testing.T) {
	root := provenanceProject(t)
	text := mcpAssertOK(t, "kern_explore", map[string]any{"root": root, "symbol": "public/a.go"})
	if !strings.Contains(text, "PublicA") {
		t.Fatalf("file explore missing symbol summary: %q", text)
	}
}

func TestExploreFileHonorsScopeGate(t *testing.T) {
	root := provenanceProject(t)
	errText := mcpToolError(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "secret/b.go",
		"scope":  deniedScope(),
	})
	if !strings.Contains(errText, "authorized read scope") {
		t.Fatalf("denied file explore error = %q, want scope refusal", errText)
	}
	if strings.Contains(errText, "SecretB") {
		t.Fatalf("denied file explore leaked content: %q", errText)
	}
	text := mcpAssertOK(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "public/a.go",
		"scope":  deniedScope(),
	})
	if !strings.Contains(text, "PublicA") {
		t.Fatalf("allowed sibling under denied scope missing summary: %q", text)
	}
}

func TestExploreFileLineRange(t *testing.T) {
	root := provenanceProject(t)
	text := mcpAssertOK(t, "kern_explore", map[string]any{
		"root":       root,
		"symbol":     "public/a.go",
		"start_line": float64(1),
		"end_line":   "2",
	})
	if !strings.HasPrefix(text, "public/a.go lines 1-2 of ") {
		t.Fatalf("missing range header: %q", text)
	}
	if n := strings.Count(text, "\n"); n != 2 {
		t.Fatalf("range 1-2 should return header + 2 lines, got %d newlines: %q", n, text)
	}
	if !strings.Contains(text, "package") {
		t.Fatalf("line 1 of a Go file should hold the package clause: %q", text)
	}
}

func TestExploreFileRangeDeniedByScope(t *testing.T) {
	root := provenanceProject(t)
	errText := mcpToolError(t, "kern_explore", map[string]any{
		"root":       root,
		"symbol":     "secret/b.go",
		"start_line": "1",
		"end_line":   "3",
		"scope":      deniedScope(),
	})
	if !strings.Contains(errText, "authorized read scope") {
		t.Fatalf("denied range read error = %q, want scope refusal", errText)
	}
}

func TestExploreFileRangePastEnd(t *testing.T) {
	root := provenanceProject(t)
	errText := mcpToolError(t, "kern_explore", map[string]any{
		"root":       root,
		"symbol":     "public/a.go",
		"start_line": "99999",
	})
	if !strings.Contains(errText, "past the end") {
		t.Fatalf("error = %q, want past-the-end", errText)
	}
}

func TestExploreSymbolStillUsesGraph(t *testing.T) {
	root := provenanceProject(t)
	text := mcpAssertOK(t, "kern_explore", map[string]any{"root": root, "symbol": "PublicA"})
	if !strings.Contains(text, "PublicA") || strings.Contains(text, " lines 1-") {
		t.Fatalf("symbol explore must stay on the graph path: %q", text)
	}
}
