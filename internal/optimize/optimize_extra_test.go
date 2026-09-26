package optimize

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/semcache"
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

// TestPromptLLMSemanticCacheModelScoped: an LLM-path result is stored under
// the model-scoped namespace prompt.llm.<model> (never the plain "prompt"
// namespace), and a near-duplicate query with the same model hits it. The
// LLM provider is unreachable, so the run exercises the llmSkipped fallback:
// the result is still deterministic and still cached.
func TestPromptLLMSemanticCacheModelScoped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = semcache.Clear("") // reset in-memory namespaces left by earlier tests
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1") // unreachable: LLM path fails gracefully
	first, err := Prompt("how do I compress a very large server log file", "", Options{Cache: true, LLM: "llama3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}
	// The entry lives in the model-scoped namespace, not the plain one.
	if n, _ := semcache.Entries("prompt.llm.llama3.2"); len(n) != 1 {
		t.Fatalf("expected 1 entry under prompt.llm.llama3.2, got %d", len(n))
	}
	if n, _ := semcache.Entries("prompt"); len(n) != 0 {
		t.Fatalf("LLM result leaked into the plain prompt namespace: %d entries", len(n))
	}
	// Near-duplicate with the same model: fuzzy hit at the 0.8 threshold.
	second, err := Prompt("how do I compress a very large server log", "", Options{Cache: true, LLM: "llama3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache || !second.SemanticHit {
		t.Fatalf("expected model-scoped semantic hit, got FromCache=%v SemanticHit=%v", second.FromCache, second.SemanticHit)
	}
	if second.Output != first.Output {
		t.Fatalf("semantic hit must reuse the stored output, got %q != %q", second.Output, first.Output)
	}
}

// TestPromptSemanticCacheCrossModelIsolation: entries under prompt.llm.<mA>
// never serve a lookup for prompt.llm.<mB> (each model gets its own
// namespace), and LLM entries never leak into the plain "prompt" namespace.
func TestPromptSemanticCacheCrossModelIsolation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = semcache.Clear("") // reset in-memory namespaces left by earlier tests
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	// Store under model-a.
	if _, err := Prompt("the database connection failed during migration", "", Options{Cache: true, LLM: "model-a"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := semcache.Entries("prompt.llm.model-a"); len(n) != 1 {
		t.Fatalf("expected 1 entry under prompt.llm.model-a, got %d", len(n))
	}
	// Same prompt, different model: must NOT fuzzy-hit model-a's entry.
	second, err := Prompt("the database connection failed during migration", "", Options{Cache: true, LLM: "model-b"})
	if err != nil {
		t.Fatal(err)
	}
	if second.FromCache {
		t.Fatalf("cross-model lookup must miss, got FromCache=%v SemanticHit=%v", second.FromCache, second.SemanticHit)
	}
	// The model-b run stored its own entry.
	if n, _ := semcache.Entries("prompt.llm.model-b"); len(n) != 1 {
		t.Fatalf("expected the model-b run to store its own entry, got %d", len(n))
	}
	// The deterministic namespace stayed untouched.
	if n, _ := semcache.Entries("prompt"); len(n) != 0 {
		t.Fatalf("LLM entries leaked into plain prompt: %d", len(n))
	}
}

// TestPromptLLMSemanticCacheHigherThreshold: the LLM namespace uses a 0.8
// threshold (vs 0.60/0.70 for the deterministic path), so a mid-similarity
// pair (~0.67) that hits the plain "prompt" cache must MISS the LLM cache.
func TestPromptLLMSemanticCacheHigherThreshold(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = semcache.Clear("") // reset in-memory namespaces left by earlier tests
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	p1 := "the database connection failed during migration"
	p2 := "the database connection failed during the migration run" // ~0.67 sim: ≥0.60, <0.80
	// Deterministic path: this pair hits at the default threshold.
	if _, err := Prompt(p1, "", Options{Cache: true}); err != nil {
		t.Fatal(err)
	}
	det, err := Prompt(p2, "", Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !det.SemanticHit {
		t.Fatalf("deterministic path must hit at 0.60, got SemanticHit=%v sim=%.3f", det.SemanticHit, det.Similarity)
	}
	// LLM path with the same pair: the 0.8 threshold rejects it.
	if _, err := Prompt(p1, "", Options{Cache: true, LLM: "llama3.2"}); err != nil {
		t.Fatal(err)
	}
	llm, err := Prompt(p2, "", Options{Cache: true, LLM: "llama3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if llm.FromCache || llm.SemanticHit {
		t.Fatalf("mid-similarity pair must MISS the 0.8 LLM threshold, got FromCache=%v SemanticHit=%v sim=%.3f", llm.FromCache, llm.SemanticHit, llm.Similarity)
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
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
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

func TestRecordBeforeBytesIsMeasuredInput(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prev := Recorder
	defer func() { Recorder = prev }()
	r, err := stats.NewRecorder()
	if err != nil {
		t.Fatal(err)
	}
	Recorder = r

	input := "record this prompt and count its raw bytes"
	res, err := Prompt(input, "", Options{Session: "bytes-test", Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}

	// The result itself must carry the measured byte counts.
	if want := len([]byte(input)); res.BeforeBytes != want {
		t.Fatalf("BeforeBytes = %d, want measured %d", res.BeforeBytes, want)
	}
	if want := len([]byte(res.Output)); res.AfterBytes != want {
		t.Fatalf("AfterBytes = %d, want measured %d", res.AfterBytes, want)
	}

	// And the recorded stats entry must persist the measured value, not a
	// reconstruction from the output plus an assumed bytes-per-token rate.
	entries, err := r.Entries(10)
	if err != nil {
		t.Fatal(err)
	}
	var found *stats.Entry
	for i := range entries {
		if entries[i].Session == "bytes-test" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected a recorded entry for the bytes-test session")
	}
	if want := len([]byte(input)); found.BeforeBytes != want {
		t.Fatalf("recorded BeforeBytes = %d, want %d", found.BeforeBytes, want)
	}
	if found.AfterBytes <= 0 {
		t.Fatalf("recorded AfterBytes should be measured > 0, got %d", found.AfterBytes)
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

// TestLogSemcacheProfileIsolation is the audit iteration-2 finding-2
// regression: a log compressed under profile A must never be fuzzy-served to
// profile B. Profiles scope the NAMESPACE (log:<profile>), so a near-duplicate
// lookup under profile B misses even though the text is nearly identical (the
// old text+profile suffix was a ~1-shingle delta and passed the 0.60 Jaccard
// threshold).
func TestLogSemcacheProfileIsolation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = semcache.Clear("")
	text := "INFO starting\nERROR panic in database connection pool\nERROR connection refused\nDEBUG worker 3\n"
	nearDup := "INFO starting service\nERROR panic in the database connection pool\nERROR connection refused again\nDEBUG worker 4\n"

	// Store under profile A (cache path accrues into "log:a").
	first, err := Log(text, Options{Cache: true, Profile: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}

	// Profile B lookup of a near-duplicate: the exact cache key differs
	// (text+profile), so this exercises the semantic layer — which must miss.
	other, err := Log(nearDup, Options{Cache: true, Profile: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if other.FromCache || other.SemanticHit {
		t.Fatalf("profile B must NOT be served profile A's entry (FromCache=%v SemanticHit=%v)", other.FromCache, other.SemanticHit)
	}

	// Same profile A, near-duplicate: semantic hit, output reused.
	again, err := Log(nearDup, Options{Cache: true, Profile: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !again.FromCache || !again.SemanticHit {
		t.Fatalf("profile A must hit its own entry, got FromCache=%v SemanticHit=%v", again.FromCache, again.SemanticHit)
	}
	if again.Output != first.Output {
		t.Fatalf("profile A semantic hit must reuse the stored output")
	}

	// The default profile is a THIRD namespace ("log:default"): nothing
	// cross-serves into it either.
	def, err := Log(nearDup, Options{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if def.FromCache || def.SemanticHit {
		t.Fatalf("default profile must not be served profile A's entry (FromCache=%v SemanticHit=%v)", def.FromCache, def.SemanticHit)
	}
}
