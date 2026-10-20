package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TestExploreBudgetedFitsSource: with a token budget, the report's verbatim
// source is fitted (folded first) so it never exceeds the budget, and the
// TokenStats savings panel is recorded.
func TestExploreBudgetedFitsSource(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)
	rep, err := ExploreBudgeted(ix, "Public", 0, 0, "", 40)
	if err != nil {
		t.Fatal(err)
	}
	if n := tokenize.Count(rep.Source); n > 40 {
		t.Errorf("fitted source = %d tokens, want <= 40", n)
	}
	if rep.Stats == nil {
		t.Fatal("budgeted explore missing TokenStats")
	}
	if rep.Stats.FullContext < rep.Stats.CompactTokens {
		t.Errorf("stats inverted: full=%d compact=%d", rep.Stats.FullContext, rep.Stats.CompactTokens)
	}
	out := RenderExplore(rep)
	if !strings.Contains(out, "tokens:") {
		t.Errorf("render missing savings panel:\n%s", out)
	}
}

// TestExploreBudgetedCalleeSkeletons: when the budget has room, folded
// skeletons of the direct callees are appended to the answer; without a
// budget they are absent (verbatim mode).
func TestExploreBudgetedCalleeSkeletons(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)

	verbatim, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(verbatim.CalleeSkels) != 0 {
		t.Errorf("verbatim explore should not carry skeletons: %v", verbatim.CalleeSkels)
	}

	budgeted, err := ExploreBudgeted(ix, "Public", 0, 0, "", 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(budgeted.CalleeSkels) == 0 {
		t.Fatalf("budgeted explore missing callee skeletons (callees=%v)", budgeted.Callees)
	}
	found := false
	for _, skel := range budgeted.CalleeSkels {
		if strings.Contains(skel, "== callee") {
			found = true
		}
	}
	if !found {
		t.Errorf("skeleton section malformed: %v", budgeted.CalleeSkels)
	}
	out := RenderExplore(budgeted)
	if !strings.Contains(out, "== callee") {
		t.Errorf("render missing callee skeleton section:\n%s", out)
	}
}

// TestRenderExploreExplainDocRenderedOnce pins the QA 2026-10-19 item 1 fix:
// with explain=true the symbol's doc comment renders EXACTLY once — the
// == source == block carries it verbatim and the == why == section
// references it instead of repeating it. Standalone FormatWhy (kern why)
// keeps the full doc. The budgeted render variant lives in
// internal/explorebudget (its cap-enforcement test is explorebudget_test.go).
func TestRenderExploreExplainDocRenderedOnce(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       "package lib\n\n// Public returns the public value.\n// It is the fixture entry point.\nfunc Public() string { return inner() }\n\nfunc inner() string { return \"x\" }\n",
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := Why(ix, "Public")
	if !ok {
		t.Fatal("Why did not resolve Public")
	}
	if info.Doc == "" {
		t.Fatal("fixture broken: Public must carry a doc comment")
	}
	docFirstLine := "Public returns the public value"

	out := RenderExploreExplain(rep, info)
	if got := strings.Count(out, docFirstLine); got != 1 {
		t.Errorf("doc comment rendered %d times, want exactly 1 (source block only):\n%s", got, out)
	}
	if !strings.Contains(out, "doc: see the == source == block above (rendered once)") {
		t.Errorf("why section must reference, not repeat, the doc:\n%s", out)
	}
	// The standalone why surface keeps the full doc.
	if !strings.Contains(FormatWhy(info), docFirstLine) {
		t.Error("standalone FormatWhy must keep the full doc comment")
	}
}
