package review

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// testIndex builds a small deterministic in-memory index:
//   - Alpha (a.go) is called by Beta (b.go, production) and TestAlpha
//     (a_test.go, test-only) -> covered.
//   - Beta (b.go) calls Alpha and Gamma, and has no callers -> uncovered.
//   - Gamma (c.go) is called by Beta (production) -> uncovered hotspot.
func testIndex(t *testing.T) *index.Index {
	t.Helper()
	ix := index.New("/virtual")
	ix.Symbols = []index.Symbol{
		{Kind: "func", Name: "Alpha", File: "a.go", Line: 5, End: 9, Lang: "go"},
		{Kind: "func", Name: "Beta", File: "b.go", Line: 3, End: 7, Lang: "go"},
		{Kind: "func", Name: "Gamma", File: "c.go", Line: 2, End: 6, Lang: "go"},
		{Kind: "func", Name: "TestAlpha", File: "a_test.go", Line: 1, End: 4, Lang: "go"},
	}
	ix.FileHashes = map[string]string{"a.go": "h1", "b.go": "h2", "c.go": "h4", "a_test.go": "h3"}
	ix.SymbolsByFile = map[string][]index.Symbol{
		"a.go":      {ix.Symbols[0]},
		"b.go":      {ix.Symbols[1]},
		"c.go":      {ix.Symbols[2]},
		"a_test.go": {ix.Symbols[3]},
	}
	ix.Calls = map[string][]index.CallEdge{
		"Beta":      {{Target: "Alpha"}, {Target: "Gamma"}},
		"TestAlpha": {{Target: "Alpha"}},
	}
	ix.Callers = map[string][]string{
		"Alpha": {"Beta", "TestAlpha"},
		"Gamma": {"Beta"},
	}
	return ix
}

func TestReviewChangesErrorHandling(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return nil, nil, context.Canceled
		},
	}
	_, err := Changes(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}
}

func TestReviewHooksErrorHandling(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
			return nil, context.Canceled
		},
	}
	_, err := Hubs(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}

	_, err = TestGaps(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}
}

func TestChangesHappy(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	out, err := Changes(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Changes failed: %v", err)
	}
	if !strings.Contains(out, "range:") || !strings.Contains(out, "a.go") {
		t.Errorf("expected change report mentioning a.go, got: %q", out)
	}
	if !strings.Contains(out, "risk") {
		t.Errorf("expected risk column in change report, got: %q", out)
	}
}

func TestChangesTestGap(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "b.go"}}, ix, nil
		},
	}
	out, err := Changes(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Changes failed: %v", err)
	}
	if !strings.Contains(out, "TEST GAP") {
		t.Errorf("expected TEST GAP marker for the uncovered change, got: %q", out)
	}
	if !strings.Contains(out, "gaps: Beta") {
		t.Errorf("expected the uncovered symbol Beta in gaps, got: %q", out)
	}
}

func TestChangesNoIndexedSymbols(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "none.go"}}, ix, nil
		},
	}
	out, err := Changes(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Changes failed: %v", err)
	}
	if !strings.Contains(out, "no indexed symbols changed") {
		t.Errorf("expected no-symbols summary, got: %q", out)
	}
}

func TestChangesEmptyChanges(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return nil, ix, nil
		},
	}
	out, err := Changes(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Changes failed: %v", err)
	}
	if !strings.Contains(out, "no indexed symbols changed") {
		t.Errorf("expected empty report, got: %q", out)
	}
}

func TestReviewHappy(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	out, err := Review(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}
	if !strings.Contains(out, "# kern review") {
		t.Errorf("expected review header, got: %q", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "Alpha") {
		t.Errorf("expected changed file and symbol, got: %q", out)
	}
}

func TestReviewMaxTokensInvalid(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	_, err := Review(ctx, h, map[string]any{"max_tokens": "abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("expected max_tokens parse error, got: %v", err)
	}
}

func TestReviewChangedContextError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return nil, nil, context.DeadlineExceeded
		},
	}
	_, err := Review(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected changed-context error, got: %v", err)
	}
}

func TestReviewWithLens(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	out, err := Review(ctx, h, map[string]any{"lens": "security"})
	if err != nil {
		t.Fatalf("Review with lens failed: %v", err)
	}
	if !strings.HasPrefix(out, "lens: security (") {
		t.Errorf("expected lens header, got: %q", out)
	}
}

func TestReviewUnknownLens(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	_, err := Review(ctx, h, map[string]any{"lens": "no-such-lens"})
	if err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Fatalf("expected unknown lens error, got: %v", err)
	}
}

func TestReviewWithProfile(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	out, err := Review(ctx, h, map[string]any{"profile": "debug"})
	if err != nil {
		t.Fatalf("Review with profile failed: %v", err)
	}
	if !strings.Contains(out, "profile: debug") {
		t.Errorf("expected debug profile marker, got: %q", out)
	}
}

func TestReviewUnknownProfile(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, ix, nil
		},
	}
	_, err := Review(ctx, h, map[string]any{"profile": "no-such-profile"})
	if err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("expected unknown profile error, got: %v", err)
	}
}

func TestHubsHappy(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	out, err := Hubs(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Hubs failed: %v", err)
	}
	if !strings.Contains(out, "hub symbols (most depended-on):") {
		t.Errorf("expected hubs header, got: %q", out)
	}
	if !strings.Contains(out, "Alpha") {
		t.Errorf("expected Alpha as a hub, got: %q", out)
	}
	if !strings.Contains(out, "bridge symbols") {
		t.Errorf("expected bridges section, got: %q", out)
	}
}

func TestHubsLimit(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	out, err := Hubs(ctx, h, map[string]any{"limit": "1"})
	if err != nil {
		t.Fatalf("Hubs failed: %v", err)
	}
	if !strings.Contains(out, "hub symbols") {
		t.Errorf("expected hubs header, got: %q", out)
	}
}

func TestHubsLimitInvalid(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	_, err := Hubs(ctx, h, map[string]any{"limit": "ten"})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("expected limit parse error, got: %v", err)
	}
}

func TestHubsEmptyIndex(t *testing.T) {
	ctx := context.Background()
	ix := index.New("/virtual")
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	out, err := Hubs(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("Hubs failed: %v", err)
	}
	if !strings.Contains(out, "hub symbols") || !strings.Contains(out, "bridge symbols") {
		t.Errorf("expected empty hubs/bridges sections, got: %q", out)
	}
}

func TestTestGapsHappy(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	out, err := TestGaps(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("TestGaps failed: %v", err)
	}
	if !strings.Contains(out, "coverage: 1/3") {
		t.Errorf("expected 1/3 coverage report, got: %q", out)
	}
	if !strings.Contains(out, "untested hotspots") || !strings.Contains(out, "Gamma") {
		t.Errorf("expected Gamma listed as untested hotspot, got: %q", out)
	}
}

func TestTestGapsLimitInvalid(t *testing.T) {
	ctx := context.Background()
	ix := testIndex(t)
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	_, err := TestGaps(ctx, h, map[string]any{"limit": "many"})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("expected limit parse error, got: %v", err)
	}
}

func TestTestGapsEmptyIndex(t *testing.T) {
	ctx := context.Background()
	ix := index.New("/virtual")
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil }}
	out, err := TestGaps(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("TestGaps failed: %v", err)
	}
	if !strings.Contains(out, "coverage: 0/0") {
		t.Errorf("expected empty coverage report, got: %q", out)
	}
}
