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

// contextSummaryTree covers every parity fixture class in one tree: ordinary
// function, method, data holder, two production definitions of one bare name,
// a uniquely fuzzy method, a test-only symbol, a production symbol sharing a
// bare name with a testdata fixture, a reference-only type alias, and an
// uncalled function.
func contextSummaryTree() map[string]string {
	return map[string]string{
		"go.mod": "module summaryfixture\n\ngo 1.22\n",
		"lib/lib.go": `package lib

// Config is a data holder.
type Config struct {
	Name string
}

// Alias is reference-only: no caller ever invokes it.
type Alias = int

// Store is a data holder with methods.
type Store struct {
	n int
}

// Build is an ordinary function.
func Build() string { return helper() }

func helper() string { return "x" }

// Orphan has no direct callers.
func Orphan() {}

func (s *Store) Get() int { return s.n }

func (s *Store) Put(v int) { s.n = v }

func (s *Store) helper() int { return s.n }
`,
		"lib/lib_test.go": `package lib

import "testing"

func TestOnlyHelper(t *testing.T) {}
`,
		"a/a.go": `package a

// Dup is the first of two production definitions of one bare name.
func Dup() {}
`,
		"b/b.go": `package b

// Dup is the second production definition of the bare name.
func Dup() {}
`,
		"bus/bus.go": `package bus

// Bus routes events.
type Bus struct{}

func (b *Bus) enqueueDeadLetter(e string) {}
`,
		"plugin/plugin.go": `package plugin

// Dispatch is the production definition.
func Dispatch() {}
`,
		"tasklife/testdata/resolve_prio/a/a.go": `package a

// Dispatch is a testdata fixture that shares the production bare name.
func Dispatch() {}
`,
	}
}

// TestContextSummaryParity pins the fast-path summary against the full
// explore report for every fixture class. The seven assertions are the plan's
// contract: resolved symbol, definition, caller count, callee count, blast
// count, affected file count, and risk tier.
func TestContextSummaryParity(t *testing.T) {
	ix := buildIndex(t, writeTree(t, contextSummaryTree()))
	depth, maxN := DefaultExploreBounds(-1, 0)

	cases := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{"ordinary function", "Build", false},
		{"method", "Store.Get", false},
		{"type or data holder", "Config", false},
		{"ambiguous bare name", "Dup", false},
		{"unique fuzzy match", "enqueue", false},
		{"no match", "zzz_nonexistent_symbol", true},
		{"test-only symbol", "TestOnlyHelper", false},
		{"production over test fixture", "Dispatch", false},
		{"reference-only symbol", "Alias", false},
		{"symbol with no direct callers", "Orphan", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sum, serr := ContextSummaryFor(ix, tc.query)
			rep, rerr := ExploreBudgeted(ix, tc.query, depth, maxN, "", 0)
			if tc.wantErr {
				if serr == nil {
					t.Fatalf("ContextSummaryFor(%q) = %+v, want error", tc.query, sum)
				}
				if rerr == nil {
					t.Fatalf("fixture broken: ExploreBudgeted(%q) did not error", tc.query)
				}
				return
			}
			if serr != nil {
				t.Fatalf("ContextSummaryFor(%q): %v", tc.query, serr)
			}
			if rerr != nil {
				t.Fatalf("ExploreBudgeted(%q): %v", tc.query, rerr)
			}

			// 1. resolved symbol
			if sum.Symbol != tc.query {
				t.Errorf("Symbol = %q, want query %q", sum.Symbol, tc.query)
			}
			if sum.Resolved != rep.Resolved {
				t.Errorf("Resolved = %q, explore = %q", sum.Resolved, rep.Resolved)
			}
			// 2. definition
			if sum.Definition.FullName() != rep.Definition.FullName() ||
				sum.Definition.Kind != rep.Definition.Kind ||
				sum.Definition.File != rep.Definition.File ||
				sum.Definition.Line != rep.Definition.Line ||
				sum.Definition.End != rep.Definition.End {
				t.Errorf("Definition = %+v, explore = %+v", sum.Definition, rep.Definition)
			}
			// 3-6. counts
			if sum.Callers != len(rep.Callers) {
				t.Errorf("Callers = %d, explore = %d (%v)", sum.Callers, len(rep.Callers), rep.Callers)
			}
			if sum.Callees != len(rep.Callees) {
				t.Errorf("Callees = %d, explore = %d (%v)", sum.Callees, len(rep.Callees), rep.Callees)
			}
			if sum.Blast != len(rep.BlastRadius) {
				t.Errorf("Blast = %d, explore = %d (%v)", sum.Blast, len(rep.BlastRadius), rep.BlastRadius)
			}
			if sum.Files != len(rep.BlastFiles) {
				t.Errorf("Files = %d, explore = %d (%v)", sum.Files, len(rep.BlastFiles), rep.BlastFiles)
			}
			// 7. risk tier — same thresholds the renderer prints.
			wantRisk := conciseRisk(len(rep.Callers), len(rep.BlastRadius))
			if sum.Risk != wantRisk {
				t.Errorf("Risk = %q, explore = %q", sum.Risk, wantRisk)
			}
			if !strings.Contains(RenderExploreConcise(rep), "risk: "+wantRisk) {
				t.Errorf("renderer risk disagrees with conciseRisk(%d, %d) = %q:\n%s",
					len(rep.Callers), len(rep.BlastRadius), wantRisk, RenderExploreConcise(rep))
			}
		})
	}
}

// TestContextSummaryFailClosed covers the summary's failure modes: nil index,
// empty symbol, no definition, and an ambiguous fuzzy query. (The graph
// accessors are total — they have no error channel — so nil/resolution are the
// observable fail-closed conditions.)
func TestContextSummaryFailClosed(t *testing.T) {
	if _, err := ContextSummaryFor(nil, "Build"); err == nil {
		t.Error("nil index must fail closed")
	}
	ix := buildIndex(t, writeTree(t, contextSummaryTree()))
	if _, err := ContextSummaryFor(ix, "   "); err == nil {
		t.Error("blank symbol must fail closed")
	}
	if _, err := ContextSummaryFor(ix, "zzz_nonexistent_symbol"); err == nil {
		t.Error("unresolvable symbol must fail closed")
	}
	// Two equally strong fuzzy candidates: the CLI treats this as ambiguous
	// and shows did-you-mean; the summary must refuse so the full path runs.
	tree := contextSummaryTree()
	tree["bus/live.go"] = "package bus\n\nfunc (b *Bus) enqueueLive(e string) {}\n"
	amb := buildIndex(t, writeTree(t, tree))
	if _, err := ContextSummaryFor(amb, "enqueue"); err == nil {
		t.Error("ambiguous fuzzy query must fail closed")
	}
}
