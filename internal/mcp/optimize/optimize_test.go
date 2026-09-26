package optimize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/semcache"
)

func TestPromptEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Prompt(ctx, map[string]any{
		"prompt": "",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected error on empty prompt, got: %v", err)
	}
}

func TestSwapEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Swap(ctx, map[string]any{
		"text": "",
	})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected error on empty text, got: %v", err)
	}
}

func TestLogEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Log(ctx, map[string]any{
		"log": "",
	})
	if err == nil || !strings.Contains(err.Error(), "log is required") {
		t.Fatalf("expected error on empty log, got: %v", err)
	}
}

func TestContextBudget(t *testing.T) {
	ctx := context.Background()
	res, err := ContextBudget(ctx, map[string]any{
		"text": "package main\n\nfunc main() {}\n",
	})
	if err != nil {
		t.Fatalf("ContextBudget failed: %v", err)
	}
	if !strings.Contains(res, "compaction skipped") {
		t.Errorf("tiny input must skip honestly, got: %s", res)
	}
}

func TestPromptHappy(t *testing.T) {
	ctx := context.Background()
	out, err := Prompt(ctx, map[string]any{
		"prompt": "explain how the dispatch loop works in plain terms",
		"cache":  "0",
		"root":   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if !strings.Contains(out, "optimized prompt (") {
		t.Errorf("expected token report or honest skip, got: %q", out)
	}
	if !strings.Contains(out, "explain how the dispatch loop works in plain terms") {
		t.Errorf("original prompt must be preserved, got: %q", out)
	}
}

func TestPromptMaskAndFewShot(t *testing.T) {
	ctx := context.Background()
	out, err := Prompt(ctx, map[string]any{
		"prompt":     "the api key is sk-abcdefghijklmnopqrstuvwxyz1234567890, use it carefully",
		"mask":       "true",
		"mask_names": "kern",
		"cache":      "0",
		"few_shot":   "true",
		"root":       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if !strings.Contains(out, "optimized prompt (") {
		t.Errorf("expected token report or honest skip, got: %q", out)
	}
}

func TestPromptExactCacheHit(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	args := map[string]any{
		"prompt":  "cache me exactly this time please",
		"cache":   "true",
		"session": "sess-1",
		"model":   "m",
		"root":    t.TempDir(),
	}
	first, err := Prompt(ctx, args)
	if err != nil {
		t.Fatalf("first Prompt failed: %v", err)
	}
	second, err := Prompt(ctx, args)
	if err != nil {
		t.Fatalf("second Prompt failed: %v", err)
	}
	if !strings.Contains(second, "served from exact cache") {
		t.Errorf("expected exact cache marker on second call, got: %q", second)
	}
	if !strings.HasPrefix(second, first) {
		t.Errorf("cache hit must return the same compressed body\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestPromptUnicode(t *testing.T) {
	ctx := context.Background()
	out, err := Prompt(ctx, map[string]any{
		"prompt": "héllo — wörld ✓ keep this intact",
		"cache":  "0",
		"root":   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if !strings.Contains(out, "optimized prompt (") {
		t.Errorf("unicode prompt must compress or skip honestly, got: %q", out)
	}
}

func TestSwapSummaryMode(t *testing.T) {
	ctx := context.Background()
	text := "plain prose without fences stays as-is"
	out, err := Swap(ctx, map[string]any{"text": text, "mode": "summary", "root": t.TempDir()})
	if err != nil {
		t.Fatalf("Swap summary failed: %v", err)
	}
	if out != text {
		t.Errorf("summary mode must be identity for unfenced text, got: %q", out)
	}
}

func TestSwapExpandMode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "x.go")
	if err := os.WriteFile(src, []byte("package x\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "```go:x.go:summary\nfunc X()\n```\n"
	out, err := Swap(ctx, map[string]any{"text": text, "mode": "expand", "root": root})
	if err != nil {
		t.Fatalf("Swap expand failed: %v", err)
	}
	if !strings.Contains(out, "package x") || !strings.Contains(out, "func X() {}") {
		t.Errorf("expand mode must restore the full file, got: %q", out)
	}
}

func TestSwapExpandMissingFile(t *testing.T) {
	ctx := context.Background()
	text := "```go:ghost.go:summary\nfunc Ghost()\n```\n"
	out, err := Swap(ctx, map[string]any{"text": text, "mode": "expand", "root": t.TempDir()})
	if err != nil {
		t.Fatalf("Swap expand failed: %v", err)
	}
	// An unresolvable file cannot be expanded; the summary block is preserved.
	if !strings.Contains(out, "ghost.go:summary") || !strings.Contains(out, "func Ghost()") {
		t.Errorf("expand mode must leave an unresolvable block unchanged, got: %q", out)
	}
}

func TestSwapDefaultFit(t *testing.T) {
	ctx := context.Background()
	text := "short text fits the default budget"
	out, err := Swap(ctx, map[string]any{"text": text, "root": t.TempDir()})
	if err != nil {
		t.Fatalf("Swap default failed: %v", err)
	}
	if out != text {
		t.Errorf("default mode must pass through small text, got: %q", out)
	}
}

func TestSwapOverBudgetWarning(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("redundant sentence that adds nothing new. ", 40)
	out, err := Swap(ctx, map[string]any{"text": long, "max_tokens": "1", "root": t.TempDir()})
	if err != nil {
		t.Fatalf("Swap failed: %v", err)
	}
	if !strings.Contains(out, "warning: still over budget after summarization") {
		t.Errorf("expected over-budget warning, got: %q", out)
	}
}

func TestSwapMaxTokensInvalid(t *testing.T) {
	ctx := context.Background()
	_, err := Swap(ctx, map[string]any{"text": "x", "max_tokens": "many", "root": t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "max_tokens: invalid integer") {
		t.Fatalf("expected max_tokens parse error, got: %v", err)
	}
}

func TestLogHappy(t *testing.T) {
	ctx := context.Background()
	log := "INFO starting worker\nERROR boom at line 1\nINFO starting worker\nERROR boom at line 1\n"
	out, err := Log(ctx, map[string]any{"log": log, "cache": "0", "root": t.TempDir()})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if !strings.Contains(out, "optimized log (") {
		t.Errorf("expected token report or honest skip, got: %q", out)
	}
}

func TestLogAdaptiveWindow(t *testing.T) {
	ctx := context.Background()
	log := "ERROR boom\n"
	out, err := Log(ctx, map[string]any{
		"log": log, "cache": "0", "root": t.TempDir(),
		"context_before": "3", "context_after": "2",
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if !strings.Contains(out, "adaptive window: -3 lines before / +2 lines after") {
		t.Errorf("expected adaptive window line, got: %q", out)
	}
}

func TestLogStructuredMarkers(t *testing.T) {
	ctx := context.Background()
	out, err := Log(ctx, map[string]any{
		"log": "WARN something odd happened\n", "cache": "0", "root": t.TempDir(),
		"structured_markers": "true",
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if !strings.Contains(out, "optimized log (") {
		t.Errorf("expected token report or honest skip, got: %q", out)
	}
}

func TestOutputEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Output(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text required error, got: %v", err)
	}
}

func TestOutputHappy(t *testing.T) {
	ctx := context.Background()
	text := "loading configuration...\nloading configuration...\nresult: 42\n"
	out, err := Output(ctx, map[string]any{"text": text})
	if err != nil {
		t.Fatalf("Output failed: %v", err)
	}
	if !strings.Contains(out, text) || !strings.Contains(out, "compaction skipped") {
		t.Errorf("tiny output must be preserved with an honest skip note, got: %q", out)
	}
}

func TestOutputUnicode(t *testing.T) {
	ctx := context.Background()
	out, err := Output(ctx, map[string]any{"text": "héllo — wörld ✓\n"})
	if err != nil {
		t.Fatalf("Output failed: %v", err)
	}
	if !strings.Contains(out, "compaction skipped") {
		t.Errorf("unicode tiny output must skip honestly, got: %q", out)
	}
}

func TestContextBudgetEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := ContextBudget(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text required error, got: %v", err)
	}
}

func TestContextBudgetMaxTokens(t *testing.T) {
	ctx := context.Background()
	out, err := ContextBudget(ctx, map[string]any{
		"text": "line one\nline two\nline three\n", "max_tokens": "20",
	})
	if err != nil {
		t.Fatalf("ContextBudget failed: %v", err)
	}
	if !strings.Contains(out, "compaction skipped") {
		t.Errorf("tiny input must skip honestly, got: %q", out)
	}
}

func TestSemcacheClearAll(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := Semcache(ctx, map[string]any{"action": "clear"})
	if err != nil {
		t.Fatalf("Semcache clear failed: %v", err)
	}
	if !strings.Contains(out, "cleared all namespaces") {
		t.Errorf("expected clear-all message, got: %q", out)
	}
}

func TestSemcacheClearNamespace(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := Semcache(ctx, map[string]any{"action": "clear", "namespace": "prompt"})
	if err != nil {
		t.Fatalf("Semcache clear failed: %v", err)
	}
	if !strings.Contains(out, "cleared prompt") {
		t.Errorf("expected cleared prompt message, got: %q", out)
	}
}

func TestSemcacheInvalidNamespace(t *testing.T) {
	ctx := context.Background()
	_, err := Semcache(ctx, map[string]any{"action": "clear", "namespace": "../escape"})
	if err == nil || !strings.Contains(err.Error(), "invalid semcache namespace") {
		t.Fatalf("expected invalid namespace error, got: %v", err)
	}
}

func TestSemcacheListRequiresNamespace(t *testing.T) {
	ctx := context.Background()
	_, err := Semcache(ctx, map[string]any{"action": "list"})
	if err == nil || !strings.Contains(err.Error(), "namespace is required") {
		t.Fatalf("expected namespace required error, got: %v", err)
	}
}

func TestSemcacheListEmpty(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := Semcache(ctx, map[string]any{"action": "list", "namespace": "empty-ns"})
	if err != nil {
		t.Fatalf("Semcache list failed: %v", err)
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("expected empty namespace message, got: %q", out)
	}
}

func TestSemcacheListWithEntries(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := semcache.Store("demo", "the stored input text", "payload"); err != nil {
		t.Fatalf("store failed: %v", err)
	}
	out, err := Semcache(ctx, map[string]any{"action": "list", "namespace": "demo"})
	if err != nil {
		t.Fatalf("Semcache list failed: %v", err)
	}
	if !strings.Contains(out, "1. the stored input text") {
		t.Errorf("expected stored entry in list, got: %q", out)
	}
}

func TestSemcacheSimilarity(t *testing.T) {
	ctx := context.Background()
	out, err := Semcache(ctx, map[string]any{
		"action": "similarity", "a": "the quick brown fox", "b": "the quick brown fox",
	})
	if err != nil {
		t.Fatalf("Semcache similarity failed: %v", err)
	}
	if !strings.Contains(out, "similarity: 1.000") {
		t.Errorf("identical inputs must have similarity 1.0, got: %q", out)
	}
}

func TestSemcacheSimilarityRequiresBoth(t *testing.T) {
	ctx := context.Background()
	_, err := Semcache(ctx, map[string]any{"action": "similarity", "a": "only-a"})
	if err == nil || !strings.Contains(err.Error(), "a and b are required") {
		t.Fatalf("expected a and b required error, got: %v", err)
	}
}

func TestSemcacheStatsEmpty(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// The semcache in-memory namespace table is process-global, so earlier
	// tests may have touched namespaces; the report must still be a valid
	// stats document (empty tree or per-namespace accounting).
	out, err := Semcache(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Semcache stats failed: %v", err)
	}
	if !strings.Contains(out, "semcache") {
		t.Errorf("expected semcache stats report, got: %q", out)
	}
	if out != "semcache: empty" && !strings.Contains(out, "entries by namespace") {
		t.Errorf("expected empty or per-namespace stats, got: %q", out)
	}
}

func TestSemcacheStatsWithNamespace(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := semcache.Store("alpha", "some input", "v"); err != nil {
		t.Fatalf("store failed: %v", err)
	}
	out, err := Semcache(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Semcache stats failed: %v", err)
	}
	if !strings.Contains(out, "entries by namespace") || !strings.Contains(out, "alpha") {
		t.Errorf("expected namespace stats, got: %q", out)
	}
}

func TestFetchAnchorRequired(t *testing.T) {
	ctx := context.Background()
	_, err := FetchAnchor(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "anchor_id is required") {
		t.Fatalf("expected anchor_id required error, got: %v", err)
	}
}

func TestFetchAnchorRoundTrip(t *testing.T) {
	ctx := context.Background()
	id := optimize.StoreAnchor("the verbatim text block")
	out, err := FetchAnchor(ctx, map[string]any{"anchor_id": id})
	if err != nil {
		t.Fatalf("FetchAnchor failed: %v", err)
	}
	if out != "the verbatim text block" {
		t.Errorf("expected stored text, got: %q", out)
	}
}

func TestFetchAnchorIDAlias(t *testing.T) {
	ctx := context.Background()
	id := optimize.StoreAnchor("aliased block")
	out, err := FetchAnchor(ctx, map[string]any{"id": id})
	if err != nil {
		t.Fatalf("FetchAnchor with id alias failed: %v", err)
	}
	if out != "aliased block" {
		t.Errorf("expected stored text via id alias, got: %q", out)
	}
}

func TestFetchAnchorMissing(t *testing.T) {
	ctx := context.Background()
	_, err := FetchAnchor(ctx, map[string]any{"anchor_id": "anchor-deadbeef"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestValidScope(t *testing.T) {
	cases := []struct {
		scope string
		want  bool
	}{
		{"", false},
		{".hidden", false},
		{"../up", false},
		{"has space", false},
		{"slash/sep", false},
		{"good", true},
		{"good-name_1.2", true},
		{"UPPER", true},
		{"123", true},
	}
	for _, c := range cases {
		if got := validScope(c.scope); got != c.want {
			t.Errorf("validScope(%q) = %v; want %v", c.scope, got, c.want)
		}
	}
}

func TestClipForMarker(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"short", "short"},
		{"", ""},
	}
	for _, c := range cases {
		if got := clipOptimize(c.in); got != c.want {
			t.Errorf("clipOptimize(%q) = %q; want %q", c.in, got, c.want)
		}
	}
	// Newlines collapse to spaces first.
	if got := clipOptimize("a\nb"); got != "a b" {
		t.Errorf("clipForMarker must collapse newlines, got: %q", got)
	}
	// At the limit stays unchanged; over the limit truncates to 57 + "...".
	at := strings.Repeat("a", 60)
	if got := clipOptimize(at); got != at {
		t.Errorf("clipForMarker at limit = %q; want unchanged", got)
	}
	over := strings.Repeat("a", 100)
	got := clipOptimize(over)
	if len(got) != 60 || !strings.HasSuffix(got, "...") {
		t.Errorf("clipForMarker over limit = %q (len %d); want 60 chars ending in ...", got, len(got))
	}
}

func TestTruncateMCP(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := truncateMCP(c.in, c.n); got != c.want {
			t.Errorf("truncateMCP(%q, %d) = %q; want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestRenderOptimize(t *testing.T) {
	res := optimize.Result{BeforeTokens: 500, AfterTokens: 200, SavedTokens: 300, SavedPercent: 60.0, Output: "out"}
	got := RenderOptimize("optimized prompt", res)
	want := "optimized prompt (tokens: 500 -> 200, saved 300 (60.0%)):\nout"
	if got != want {
		t.Errorf("renderOptimize = %q; want %q", got, want)
	}
}

// TestRenderOptimizeBelowFloorHonest pins the N5 residual: a tiny input
// (below the token floor) that would otherwise report a marginal "saved P%"
// claim — whose metadata header makes the total output larger — is skipped
// with the honest note instead.
func TestRenderOptimizeBelowFloorHonest(t *testing.T) {
	res := optimize.Result{BeforeTokens: 14, AfterTokens: 11, SavedTokens: 3, SavedPercent: 21.4, Output: "tiny context"}
	got := RenderOptimize("optimized prompt", res)
	if !strings.Contains(got, "compaction skipped: no token savings") {
		t.Errorf("below-floor result must skip honestly, got: %q", got)
	}
	if !strings.Contains(got, "tiny context") {
		t.Errorf("original output must be preserved, got: %q", got)
	}
	if strings.Contains(got, "saved") || strings.Contains(got, "%") {
		t.Errorf("must not claim a savings percentage, got: %q", got)
	}
}
