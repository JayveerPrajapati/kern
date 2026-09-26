package optimize

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TestOutputCompactionBelowFloorHonest pins N5: a tiny input (under the
// token floor) is returned verbatim with an honest skip note — never a
// "36 -> 51 (0% saved)" style claim where the metadata overhead made the
// output larger and a percentage was still reported.
func TestOutputCompactionBelowFloorHonest(t *testing.T) {
	out, err := Output(context.Background(), map[string]any{"text": "tiny output"})
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if !strings.Contains(out, "tiny output") {
		t.Errorf("original text must be preserved, got: %q", out)
	}
	if !strings.Contains(out, "compaction skipped: output below floor") {
		t.Errorf("expected the below-floor skip note, got: %q", out)
	}
	if strings.Contains(out, "saved") {
		t.Errorf("must not claim a savings percentage, got: %q", out)
	}
}

// TestContextBudgetBelowFloorHonest pins the same floor on the token-budget
// fitter.
func TestContextBudgetBelowFloorHonest(t *testing.T) {
	out, err := ContextBudget(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("ContextBudget: %v", err)
	}
	if !strings.Contains(out, "compaction skipped: output below floor") {
		t.Errorf("expected the below-floor skip note, got: %q", out)
	}
	if strings.Contains(out, "saved") {
		t.Errorf("must not claim a savings percentage, got: %q", out)
	}
}

// TestContextBudgetNoSavingsHonest pins N5 on the growing/no-op case: an
// input above the floor that compaction would not shrink (FitCode is a no-op
// when the text already fits the budget) is returned verbatim with an honest
// skip note instead of a "saved 0%" claim.
func TestContextBudgetNoSavingsHonest(t *testing.T) {
	text := strings.Repeat("func alpha() { return 1 }\n", 100) // well above the token floor
	if tokenize.Count(text) < compactFloorTokens {
		t.Fatalf("precondition failed: fixture must exceed the %d-token floor", compactFloorTokens)
	}
	out, err := ContextBudget(context.Background(), map[string]any{"text": text, "max_tokens": "100000"})
	if err != nil {
		t.Fatalf("ContextBudget: %v", err)
	}
	if !strings.Contains(out, text) {
		t.Errorf("original text must be preserved")
	}
	if !strings.Contains(out, "compaction skipped") {
		t.Errorf("expected the honest skip note, got: %q", out[:120])
	}
	if strings.Contains(out, "saved") {
		t.Errorf("must not claim a savings percentage, got: %q", out[:120])
	}
}

// TestRenderOptimizeNoSavingsHonest pins N5 on the prompt/log summary line:
// a result whose "compaction" grew (after >= before) reports the skip, not a
// negative "saved" percentage.
func TestRenderOptimizeNoSavingsHonest(t *testing.T) {
	res := optimize.Result{BeforeTokens: 36, AfterTokens: 51, SavedTokens: -15, SavedPercent: -41.7, Output: "original text"}
	out := RenderOptimize("optimized prompt", res)
	if !strings.Contains(out, "compaction skipped: no token savings") {
		t.Errorf("expected the honest skip note, got: %q", out)
	}
	if !strings.Contains(out, "original text") {
		t.Errorf("original output must be preserved, got: %q", out)
	}
	if strings.Contains(out, "saved") || strings.Contains(out, "%") {
		t.Errorf("must not claim a savings percentage, got: %q", out)
	}
}
