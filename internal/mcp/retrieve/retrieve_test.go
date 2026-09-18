package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
)

func TestParseRetrieveLevel(t *testing.T) {
	for in, want := range map[string]retrieval.Level{
		"":     retrieval.L2, // schema default
		"l1":   retrieval.L1,
		"l2":   retrieval.L2,
		"l3":   retrieval.L3,
		"L1":   retrieval.L1,
		" l2 ": retrieval.L2,
	} {
		got, err := parseRetrieveLevel(in)
		if err != nil {
			t.Errorf("parseRetrieveLevel(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseRetrieveLevel(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseRetrieveLevel("l4"); err == nil {
		t.Error("parseRetrieveLevel(l4): want error, got nil")
	}
	if _, err := parseRetrieveLevel("bogus"); err == nil {
		t.Error("parseRetrieveLevel(bogus): want error, got nil")
	}
}

func TestRenderWithHandle(t *testing.T) {
	h := retrieval.NewHandle(retrieval.TypeSymbol, "Alpha", "a.go", 3, 100, 0.9, "hash")

	// Source handle path (L3).
	res := &retrieval.Result{Level: retrieval.L3, Source: &retrieval.Source{Handle: h, Text: "func Alpha() {}\n"}}
	out := renderWithHandle(res)
	if !strings.Contains(out, "handle ") || !strings.Contains(out, "(resolve with kern_resolve)") {
		t.Errorf("L3 render missing handle line:\n%s", out)
	}
	// The handle id is truncated to 8 chars, matching what L1 renders.
	id8 := h.ID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	if !strings.Contains(out, "handle "+id8+" Alpha a.go:3") {
		t.Errorf("handle line mismatch:\n%s", out)
	}

	// Detail handle path (L2).
	dres := &retrieval.Result{Level: retrieval.L2, Detail: &retrieval.Detail{Handle: h, Callers: []string{"Beta"}}}
	dout := renderWithHandle(dres)
	if !strings.Contains(dout, "handle ") {
		t.Errorf("L2 render missing handle line:\n%s", dout)
	}

	// No handle anywhere -> the L2 render contract returns "" (Render needs
	// Detail.Handle to render a neighborhood), so nothing is appended.
	nores := &retrieval.Result{Level: retrieval.L2, Detail: &retrieval.Detail{Callers: []string{"Beta"}}}
	nout := renderWithHandle(nores)
	if nout != "" {
		t.Errorf("handle-less L2 result = %q, want empty (Render contract)", nout)
	}

	// Nil result renders empty.
	if got := renderWithHandle(nil); got != "" {
		t.Errorf("nil result = %q, want empty", got)
	}
}

// TestRetrieveValidationErrors pins the fail-closed argument validation
// before any index/retrieval work (nil index is safe on these paths).
func TestRetrieveValidationErrors(t *testing.T) {
	gvc := mcpgov.GovContext{}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"level+task_type conflict", map[string]any{"task_type": "refactor", "level": "l2", "symbol": "A"}},
		{"l1 without query", map[string]any{"level": "l1"}},
		{"l2 without symbol", map[string]any{"level": "l2"}},
		{"l3 without symbol", map[string]any{"level": "l3"}},
		{"task_type without symbol", map[string]any{"task_type": "refactor"}},
		{"invalid level", map[string]any{"level": "l9", "symbol": "A"}},
	}
	for _, c := range cases {
		if _, err := Retrieve(context.Background(), nil, gvc, c.args); err == nil {
			t.Errorf("%s: want error, got nil", c.name)
		}
	}
}

// TestResolveValidationErrors pins Resolve's fail-closed paths.
func TestResolveValidationErrors(t *testing.T) {
	gvc := mcpgov.GovContext{}
	if _, err := Resolve(context.Background(), nil, gvc, map[string]any{}); err == nil {
		t.Error("Resolve without handle: want error, got nil")
	} else if !strings.Contains(err.Error(), "handle is required") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := Resolve(context.Background(), nil, gvc, map[string]any{"handle": "nope-nope"}); err == nil {
		t.Error("Resolve with unknown handle: want error, got nil")
	} else if !strings.Contains(err.Error(), "unknown handle") {
		t.Errorf("unexpected error: %v", err)
	}
}

// rawGov returns a GovContext in raw mode: nil governor, no-op stamping.
func rawGov() mcpgov.GovContext {
	return mcpgov.GovContext{
		NewGov:   func() (*mcpgov.Governor, error) { return nil, nil },
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
}

// TestRetrieveL1RoundTrip drives the full governed L1 path with a real index
// and a raw-mode governor: items render with handles.
func TestRetrieveL1RoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Alpha() {}\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Retrieve(context.Background(), ix, rawGov(), map[string]any{
		"query": "Alpha",
		"level": "l1",
	})
	if err != nil {
		t.Fatalf("Retrieve L1 error: %v", err)
	}
	if !strings.Contains(out, "== level 1:") || !strings.Contains(out, "Alpha") {
		t.Errorf("L1 render missing expected content:\n%s", out)
	}
}
