package mcpcli

import (
	"strings"
	"testing"
)

// TestParseSemanticBoolInline pins the drift fix from the consolidation
// (tranche 7): bool flags honor inline values exactly like the unified
// cmd/kern parser's setBool — --apply=false sets FALSE, and a garbage inline
// value is a parse error. Before the fix the mirrors ignored inline values
// entirely (--apply=false silently set true).
func TestParseSemanticBoolInline(t *testing.T) {
	f, _, err := parseSemanticFlags([]string{"--apply=false", "--json=true"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if f.apply {
		t.Fatalf("--apply=false set apply=true")
	}
	if !f.json {
		t.Fatalf("--json=true set json=false")
	}

	_, _, err = parseSemanticFlags([]string{"--apply=bogus"})
	if err == nil || !strings.Contains(err.Error(), `invalid boolean value`) {
		t.Fatalf("err = %v, want invalid boolean value", err)
	}
}

// TestParsePreeditUnifiedSemantics pins the family-level view of the shared
// semantics: positionals, single-dash normalization, unknown flags, and the
// value-less trailing int.
func TestParsePreeditUnifiedSemantics(t *testing.T) {
	f, rest, err := parsePreeditFlags([]string{"main.go", "-lines", "3", "--symbol", "Do"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if f.lines != 3 || f.symbol != "Do" {
		t.Fatalf("flags = %+v, want lines=3 symbol=Do", f)
	}
	if len(rest) != 1 || rest[0] != "main.go" {
		t.Fatalf("rest = %v, want [main.go]", rest)
	}

	_, _, err = parsePreeditFlags([]string{"--lines"})
	if err != nil {
		t.Fatalf("trailing value-less --lines must accept-and-default, got %v", err)
	}
	_, _, err = parsePreeditFlags([]string{"--nope"})
	if err == nil || err.Error() != "unknown flag: --nope" {
		t.Fatalf("err = %v, want unknown flag: --nope", err)
	}
}

// TestParseContextWatchBudgetQuirk pins the historical quirk (preserved
// deliberately): a value-less --budget sets budgetSet=true with budget=0 —
// selecting budget 0, not the 32000 default.
func TestParseContextWatchBudgetQuirk(t *testing.T) {
	f, _, err := parseContextWatchFlags([]string{"--budget"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !f.budgetSet || f.budget != 0 {
		t.Fatalf("budgetSet=%v budget=%d, want true/0", f.budgetSet, f.budget)
	}
}

// TestRunAstTransformBadBoolInlineExits2 pins the end-to-end contract: a
// garbage inline bool value surfaces as a usage error (exit 2).
func TestRunAstTransformBadBoolInlineExits2(t *testing.T) {
	if code := RunAstTransform([]string{"--apply=bogus"}); code != 2 {
		t.Fatalf("RunAstTransform(--apply=bogus) = %d, want 2 (usage error)", code)
	}
}
