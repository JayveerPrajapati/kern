package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

// TestPrintTokenSavingsOnlyOnRealSavings pins F11: the "kern: X -> Y tokens
// (saved N, P%…)" accounting line prints only on a genuine reduction; equal
// or inflated results are silent (no "saved 0, 0.0%" noise).
func TestPrintTokenSavingsOnlyOnRealSavings(t *testing.T) {
	var b strings.Builder
	printTokenSavings(&b, 1000, 400, ", budget 4000", " [window -1/+1]")
	want := "kern: 1000 -> 400 tokens (saved 600, 60.0%, budget 4000) [window -1/+1]\n"
	if b.String() != want {
		t.Errorf("reduced: got %q, want %q", b.String(), want)
	}
	for _, c := range []struct {
		name          string
		before, after int
	}{
		{"equal", 500, 500},
		{"inflated", 500, 600},
	} {
		var silent strings.Builder
		printTokenSavings(&silent, c.before, c.after, ", budget 4000", " [window -1/+1]")
		if silent.String() != "" {
			t.Errorf("%s: expected silence, got %q", c.name, silent.String())
		}
	}
}

// TestWireRecorder pins wireRecorder's contract: it must not panic and must be
// safe to call repeatedly. XDG_CACHE_HOME is isolated so the stats recorder
// (rooted at <cache>/kern/stats) never touches the real user cache.
func TestWireRecorder(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	wireRecorder()
	wireRecorder() // repeated calls must be safe

	if optimize.Recorder == nil {
		t.Fatal("wireRecorder: optimize.Recorder not wired")
	}
}

// TestClipJSONStringsTruncatesAbsurdFields pins the F4 safety guard: the
// printJSON input walker must keep --json payloads valid even when a field is
// absurdly large (embedded test log), truncating the oversized string with a
// visible marker instead of dumping multi-MB strings to the terminal.
func TestClipJSONStringsTruncatesAbsurdFields(t *testing.T) {
	big := strings.Repeat("x", maxJSONFieldLen+100)
	clipped := clipJSONStrings(map[string]any{"output": big, "ok": true, "count": 12345})
	b, err := json.Marshal(clipped)
	if err != nil {
		t.Fatalf("clipped payload does not marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("clipped payload is not valid JSON: %v", err)
	}
	got, _ := decoded["output"].(string)
	if len(got) >= len(big) {
		t.Errorf("absurd field not truncated (len %d, want < %d)", len(got), len(big))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncated field missing marker, got: %.60s", got)
	}
	if decoded["ok"] != true || decoded["count"] != float64(12345) {
		t.Errorf("non-string fields altered by the guard: ok=%v count=%v", decoded["ok"], decoded["count"])
	}
	// Small strings and nil pass through unchanged.
	if got := clipJSONStrings("hello").(string); got != "hello" {
		t.Errorf("small string altered: %q", got)
	}
	if clipJSONStrings(nil) != nil {
		t.Error("nil payload altered by the guard")
	}
}

// TestClipJSONStringsPreservesTime pins the dogfooding B-LOW fix: the guard
// must NOT zero time.Time values. The old struct-recursion copied only
// CanInterface() fields, and time.Time has none exported — so every
// built_at/checked_at in --json payloads (index --status freshness_proof,
// audit, evidence) silently reset to the epoch.
func TestClipJSONStringsPreservesTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	clipped := clipJSONStrings(map[string]any{"checked_at": now})
	m, ok := clipped.(map[string]any)
	if !ok {
		t.Fatalf("clipJSONStrings(map) = %T, want map[string]any", clipped)
	}
	got, ok := m["checked_at"].(time.Time)
	if !ok {
		t.Fatalf("clipped checked_at = %T, want time.Time", m["checked_at"])
	}
	if got.IsZero() {
		t.Fatal("time.Time was zeroed by the guard — timestamp lost")
	}
	if !got.Equal(now) {
		t.Errorf("time altered: got %v, want %v", got, now)
	}
}

// TestSuggestSymbolsCaseVariantCamelCase locks F2: `kern explore
// sanitizeDocName` (lookup fails) must suggest the case-variant camelCase
// symbol `SanitizeDocName` — the ranked-search hits added at the top of
// suggestSymbols catch what the anchored Search misses.
func TestSuggestSymbolsCaseVariantCamelCase(t *testing.T) {
	root := contextSymbolFixture(t, `// SanitizeDocName cleans a doc name.
func SanitizeDocName(s string) string { return s }
// sandboxExecPath resolves the sandbox binary.
func sandboxExecPath() string { return "" }
`)
	ix, err := loadOrBuild(root)
	if err != nil {
		t.Fatalf("loadOrBuild: %v", err)
	}
	got := suggestSymbols(ix, "sanitizeDocName")
	found := false
	for _, s := range got {
		if s == "SanitizeDocName" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggestSymbols(%q) = %v, want SanitizeDocName included", "sanitizeDocName", got)
	}
}

// TestVerifyTypeVocabularyAcceptsReuse pins CLI-surface parity with the
// engine's "reuse" advisory check (VerifyTypesKnown / KnownVerifyType in
// internal/mcp/meta accept it): both the type keyword list behind
// validVerifyType and the isVerifyTypes disambiguation gate must accept
// "reuse", so `kern verify reuse` runs the check instead of being misread
// as the claims-verification form or rejected as unknown.
func TestVerifyTypeVocabularyAcceptsReuse(t *testing.T) {
	if !validVerifyType("reuse") {
		t.Error(`validVerifyType("reuse") = false, want true (engine accepts the reuse advisory type)`)
	}
	if !isVerifyTypes("reuse") {
		t.Error(`isVerifyTypes("reuse") = false, want true (kern verify reuse would be misread as claims verification)`)
	}
	if isVerifyTypes("reuse,bogus") {
		t.Error(`isVerifyTypes("reuse,bogus") = true, want false (unknown members must still be rejected)`)
	}
}
