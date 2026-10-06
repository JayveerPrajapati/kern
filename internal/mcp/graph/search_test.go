package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// rawGVC is the minimal raw-mode bundle used by Search/Explore tests: a nil
// governor (no filtering) with no-op stamps, so results come back unfiltered
// and the NewGov/StampGov hooks never touch a real scope.
func rawGVC() gov.GovContext {
	return gov.GovContext{
		NewGov:   func() (*gov.Governor, error) { return nil, nil },
		StampGov: func(*gov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
}

// weakOnlyFixture writes a tiny repo whose symbols fuzzy-match a nonsense
// query ONLY through the file path (ranked search's lowest tier), so the
// hits are real but none of their NAMES contain the query — the exact
// clean-miss scenario the remediation targets ("zzqxyw" returning test
// function names). Every symbol lives in a file whose name carries the
// nonsense token, so the P0-1 identifier gate (which would zero the hits)
// stays satisfied via MatchedAll.
func weakOnlyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zzqxyw_a_test.go", "package main\n\n// TestAlpha is unrelated to the query.\nfunc TestAlpha() {}\n")
	write("zzqxyw_b_test.go", "package main\n\n// TestBeta is unrelated to the query.\nfunc TestBeta() {}\n")
	write("zzqxyw_c_test.go", "package main\n\n// TestGamma is unrelated to the query.\nfunc TestGamma() {}\n")
	write("zzqxyw_d_test.go", "package main\n\n// TestDelta is unrelated to the query.\nfunc TestDelta() {}\n")
	return root
}

// TestSearchNoStrongMatchSignal pins the clean-miss signal (remediation): a
// nonsense query that only fuzzy-matches through the file path must NOT be
// served as a valid hit list. The response names the clipped query and the
// weak matches with an explicit low-confidence framing instead.
func TestSearchNoStrongMatchSignal(t *testing.T) {
	root := weakOnlyFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Search(context.Background(), ix, rawGVC(), map[string]any{"root": root, "query": "zzqxyw"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no symbols matched") {
		t.Fatalf("precondition failed: fixture must yield fuzzy hits, got: %q", out)
	}
	if !strings.Contains(out, "no strong symbol matches for: zzqxyw") {
		t.Fatalf("expected the no-strong-match line, got: %q", out)
	}
	if !strings.Contains(out, "weak fuzzy matches (low confidence, may be unrelated):") {
		t.Fatalf("expected the weak-match framing, got: %q", out)
	}
}

// TestSearchNoStrongMatchSignalListsTop3 pins the top-3 cap: the weak-match
// hint lists at most three names even when the fuzzy pile is larger.
func TestSearchNoStrongMatchSignalListsTop3(t *testing.T) {
	root := weakOnlyFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Search(context.Background(), ix, rawGVC(), map[string]any{"root": root, "query": "zzqxyw"})
	if err != nil {
		t.Fatal(err)
	}
	marker := "weak fuzzy matches (low confidence, may be unrelated): "
	idx := strings.Index(out, marker)
	if idx < 0 {
		t.Fatalf("missing weak-match marker: %q", out)
	}
	listed := strings.Split(out[idx+len(marker):], ", ")
	if len(listed) > 3 {
		t.Fatalf("weak-match hint must cap at 3 names, got %d: %q", len(listed), out)
	}
	if len(listed) == 0 {
		t.Fatalf("weak-match hint must list at least one name, got: %q", out)
	}
}

// TestSearchStrongMatchReturnsResultsUnchanged pins the conservative side:
// a real query with at least one strong containment match keeps the full hit
// list exactly as before — no no-strong framing, no truncation.
func TestSearchStrongMatchReturnsResultsUnchanged(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Search(context.Background(), ix, rawGVC(), map[string]any{"root": root, "query": "ServeMux"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no strong symbol matches") {
		t.Fatalf("real query must not hit the no-strong path: %q", out)
	}
	if !strings.Contains(out, "func ServeMux") {
		t.Fatalf("expected the ServeMux hit row, got: %q", out)
	}
	if !strings.Contains(out, "main.go:") {
		t.Fatalf("expected the hit file:line, got: %q", out)
	}
}

// TestSearchPartialNameStillReturnsResults pins the partial-name case: a
// legitimately fuzzy query ("handle" matches handleHealth/handleUsers via
// containment) still returns its results unchanged — only NO-strong-match
// queries are suppressed.
func TestSearchPartialNameStillReturnsResults(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Search(context.Background(), ix, rawGVC(), map[string]any{"root": root, "query": "handle"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no strong symbol matches") {
		t.Fatalf("partial-name query must not hit the no-strong path: %q", out)
	}
	if !strings.Contains(out, "handleHealth") {
		t.Fatalf("expected the handleHealth hit row, got: %q", out)
	}
}

// TestSearchSpaceVariantStillStrong pins the naming-variant normalization: a
// space-separated query ("serve mux") is the same symbol as the camelCase
// name (ServeMux) once normalized, so it must stay a strong match and keep
// its results — never the no-strong message.
func TestSearchSpaceVariantStillStrong(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Search(context.Background(), ix, rawGVC(), map[string]any{"root": root, "query": "serve mux"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no strong symbol matches") {
		t.Fatalf("space-variant query must not hit the no-strong path: %q", out)
	}
	if !strings.Contains(out, "ServeMux") {
		t.Fatalf("expected the ServeMux hit row, got: %q", out)
	}
}
