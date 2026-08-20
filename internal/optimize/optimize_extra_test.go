package optimize

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/stats"
)

func TestPromptEmptyInput(t *testing.T) {
	if _, err := Prompt("   ", "", Options{}); err == nil {
		t.Fatal("expected error for empty prompt+log")
	}
}

func TestPromptAttachedLog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := Prompt("fix the crash", "INFO starting\nERROR boom\nDEBUG trace", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "attached log (compressed)") {
		t.Fatalf("expected compressed log marker, got %q", res.Output)
	}
	if res.FromCache {
		t.Fatal("did not expect cache hit")
	}
	if res.BeforeTokens <= 0 || res.AfterTokens <= 0 {
		t.Fatalf("expected positive token counts, got %+v", res)
	}
}

func TestPromptCacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prompt := "cache me this prompt please"

	first, err := Prompt(prompt, "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}

	second, err := Prompt(prompt, "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Fatal("second identical call should be served from cache")
	}
	if second.Output != first.Output {
		t.Fatalf("cached output mismatch: %q != %q", second.Output, first.Output)
	}
}

func TestPromptSemanticCacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first, err := Prompt("how do I compress a very large server log file", "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}

	// Near-duplicate (one word removed): exact hash misses, semantic cache hits.
	second, err := Prompt("how do I compress a very large server log", "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache || !second.SemanticHit {
		t.Fatalf("expected semantic cache hit, got FromCache=%v SemanticHit=%v", second.FromCache, second.SemanticHit)
	}
	if second.Similarity <= 0 || second.MatchedInput == "" {
		t.Fatalf("expected similarity + matched input, got %+v", second)
	}
	if second.Output != first.Output {
		t.Fatalf("semantic hit must reuse the stored output, got %q != %q", second.Output, first.Output)
	}

	// Disjoint request must NOT hit the semantic cache.
	third, err := Prompt("buy cheap luxury apartments in zurich", "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if third.FromCache {
		t.Fatalf("disjoint prompt should miss, got FromCache=%v", third.FromCache)
	}
}

func TestLogSemanticCacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first, err := Log("INFO starting\nERROR panic in database connection pool\nERROR connection refused\nDEBUG worker 3\n", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}

	// Same recurring error, slightly different context lines: near-duplicate.
	second, err := Log("INFO starting service\nERROR panic in the database connection pool\nERROR connection refused again\nDEBUG worker 4\n", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache || !second.SemanticHit {
		t.Fatalf("expected semantic log hit, got FromCache=%v SemanticHit=%v", second.FromCache, second.SemanticHit)
	}
	if second.Output != first.Output {
		t.Fatalf("semantic log hit must reuse stored output")
	}
}

func TestPromptCacheKeyDiffersByModel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prompt := "model-scoped cache check"

	first, err := Prompt(prompt, "", Options{Cache: true, Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := Prompt(prompt, "", Options{Cache: true, Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	// Compression is deterministic and model-independent, so the semantic
	// cache may serve a different-model request the same output — that is the
	// point (similar query -> instant). The exact cache stays model-scoped.
	if !other.FromCache {
		t.Fatal("semantic cache should serve the identical deterministic result regardless of model name")
	}
	if other.Output != first.Output {
		t.Fatalf("model-independent output mismatch: %q != %q", other.Output, first.Output)
	}
}

func TestPromptMaskRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prompt := "contact admin@example.com from 192.168.0.10 and use the acme token"

	res, err := Prompt(prompt, "", Options{Mask: true, MaskNames: []string{"acme"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "[MASKED_") {
		t.Fatalf("placeholders must be restored after unmask, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "acme") {
		t.Fatalf("custom masked name should survive round-trip, got %q", res.Output)
	}
}

func TestPromptLLMFallback(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := Prompt("deterministic fallback path", "", Options{LLM: "llama3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Output) == "" {
		t.Fatal("expected deterministic output when Ollama is unreachable")
	}
}

func TestLogEmpty(t *testing.T) {
	if _, err := Log("  ", Options{}); err == nil {
		t.Fatal("expected error for empty log")
	}
}

func TestLogCompresses(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	text := strings.Repeat("noise line that is not important at all\n", 50) + "ERROR real failure"
	res, err := Log(text, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "ERROR real failure") {
		t.Fatalf("critical line dropped, got %q", res.Output)
	}
	if res.SavedTokens <= 0 {
		t.Fatalf("expected token savings, got %+v", res)
	}
}

func TestRunBuildEmpty(t *testing.T) {
	if _, err := RunBuild(context.Background(), " ", "", Options{}); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestRunBuildOutput(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := RunBuild(context.Background(), "echo hello build", t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "cmd: echo hello build") {
		t.Fatalf("expected cmd: prefix, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "hello build") {
		t.Fatalf("command output missing, got %q", res.Output)
	}
}

func TestRunBuildFailure(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := RunBuild(context.Background(), "exit 3", t.TempDir(), Options{})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(res.Output, "exit status 3") {
		t.Fatalf("error not surfaced, got %q", res.Output)
	}
}

func TestCompactCommandOutputFiltersNoise(t *testing.T) {
	out := compactCommandOutput(strings.Join([]string{
		"[INFO] starting work",
		"[DEBUG] tracing internals",
		"Downloading 100%",
		"progress: 10/10",
		"real warning: disk full",
		"",
		"error: build failed",
		"plain useful line",
	}, "\n"))
	got := strings.Split(out, "\n")
	for _, want := range []string{"real warning: disk full", "error: build failed", "plain useful line"} {
		if !contains(got, want) {
			t.Fatalf("expected %q kept, got %q", want, got)
		}
	}
	for _, drop := range []string{"[INFO]", "[DEBUG]", "Downloading", "progress:"} {
		if contains(got, drop) {
			t.Fatalf("expected %q filtered, got %q", drop, got)
		}
	}
}

func TestRecordWritesStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prev := Recorder
	defer func() { Recorder = prev }()

	r, err := stats.NewRecorder()
	if err != nil {
		t.Fatal(err)
	}
	Recorder = r

	if _, err := Prompt("record this prompt", "", Options{Session: "opt-test", Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.Summarize(7, "opt-test")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations < 1 {
		t.Fatalf("expected recorded operation, got %+v", sum)
	}
	if sum.ByOperation[stats.OpOptimizePrompt] < 1 {
		t.Fatalf("expected optimize_prompt op, got %+v", sum.ByOperation)
	}
}

func TestRecordNilRecorderIsNoop(t *testing.T) {
	prev := Recorder
	defer func() { Recorder = prev }()
	Recorder = nil
	if _, err := Prompt("noop recorder", "", Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestModelOrDefault(t *testing.T) {
	if got := modelOrDefault(""); got != stats.DefaultModel {
		t.Fatalf("empty model should default to %s, got %s", stats.DefaultModel, got)
	}
	if got := modelOrDefault("custom"); got != "custom" {
		t.Fatalf("expected custom model passthrough, got %s", got)
	}
}

func TestPctEdgeCases(t *testing.T) {
	if pct(0, 5) != 0 {
		t.Fatal("pct must be 0 when before is 0")
	}
	if got := pct(100, 25); got != 75 {
		t.Fatalf("expected 75, got %f", got)
	}
}

func TestPromptRecordsRunBuildStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prev := Recorder
	defer func() { Recorder = prev }()
	r, err := stats.NewRecorder()
	if err != nil {
		t.Fatal(err)
	}
	Recorder = r
	if _, err := RunBuild(context.Background(), "true", t.TempDir(), Options{Session: "build-test"}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.Summarize(7, "build-test")
	if err != nil {
		t.Fatal(err)
	}
	if sum.ByOperation[stats.OpRunBuild] < 1 {
		t.Fatalf("expected run_build op, got %+v", sum.ByOperation)
	}
}

func TestFinishComputesSavings(t *testing.T) {
	res := finish("aaaa bbbb cccc", "aaaa", 0)
	if res.BeforeTokens <= res.AfterTokens {
		t.Fatalf("expected savings, got %+v", res)
	}
	if res.SavedPercent <= 0 {
		t.Fatalf("expected positive saved percent, got %+v", res)
	}
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}
