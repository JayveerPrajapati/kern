package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

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
