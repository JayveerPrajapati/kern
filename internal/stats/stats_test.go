package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCostPerMillion(t *testing.T) {
	if got := CostPerMillion("gpt-4o"); got != 2.50 {
		t.Fatalf("expected 2.50, got %f", got)
	}
	if got := CostPerMillion("local"); got != 0 {
		t.Fatalf("local must be free, got %f", got)
	}
	if got := CostPerMillion("unknown-model"); got != 0 {
		t.Fatalf("unknown model must be 0, got %f", got)
	}
	if got := CostPerMillion(""); got != 0 {
		t.Fatalf("empty model must be 0, got %f", got)
	}
}

func TestRecorderRecordAndSummarize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}

	if err := r.Record(Entry{
		Time:         time.Now().Add(-1 * time.Hour),
		Session:      "s1",
		Operation:    OpOptimizePrompt,
		Model:        "gpt-4o",
		BeforeTokens: 1000,
		AfterTokens:  400,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(Entry{
		Time:         time.Now(),
		Session:      "s2",
		Operation:    OpRunBuild,
		BeforeTokens: 10,
		AfterTokens:  10,
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 2 {
		t.Fatalf("expected 2 ops, got %d", sum.Operations)
	}
	if sum.BeforeTotal != 1010 || sum.AfterTotal != 410 {
		t.Fatalf("token totals wrong: %+v", sum)
	}
	if sum.ByOperation[OpOptimizePrompt] != 1 || sum.ByOperation[OpRunBuild] != 1 {
		t.Fatalf("by-operation counts wrong: %+v", sum.ByOperation)
	}
	// 600 tokens saved, 600/1e6 * $2.50 = $0.0015
	if sum.CostSaved < 0.0014 || sum.CostSaved > 0.0016 {
		t.Fatalf("cost estimate off: %f", sum.CostSaved)
	}
	if sum.SavedPct < 59 || sum.SavedPct > 60 {
		t.Fatalf("expected ~59.4%% saved, got %f", sum.SavedPct)
	}

	// Session filter: only s2 remains.
	sumS2, err := r.Summarize(7, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if sumS2.Operations != 1 || sumS2.ByOperation[OpRunBuild] != 1 {
		t.Fatalf("session filter wrong: %+v", sumS2)
	}
}

func TestSummarizeEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 0 || sum.ByOperation == nil {
		t.Fatalf("expected empty summary with nil map, got %+v", sum)
	}
}

func TestSummarizeSkipsBadFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not a date file and not JSONL -> ignored.
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644)
	// Date-named but invalid JSONL -> ignored.
	_ = os.WriteFile(filepath.Join(dir, "2026-08-06.jsonl"), []byte("not json\n"), 0o644)
	// Nested dir -> ignored.
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	r := &Recorder{dir: dir}
	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 0 {
		t.Fatalf("expected 0 ops from garbage files, got %d", sum.Operations)
	}
}

func TestSummarizeRespectsDayWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	r := &Recorder{dir: dir}

	// Stale entry in an old file.
	oldDay := filepath.Join(dir, time.Now().AddDate(0, 0, -10).Format("2006-01-02")+".jsonl")
	if err := os.MkdirAll(filepath.Dir(oldDay), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(oldDay, []byte(`{"operation":"optimize_prompt","before_tokens":5,"after_tokens":1,"saved_tokens":4,"cost_saved_usd":0}`+"\n"), 0o644)

	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 0 {
		t.Fatalf("stale entry must be excluded from 7-day window, got %d", sum.Operations)
	}
}

func TestRecordZeroesTime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	if err := r.Record(Entry{Operation: OpOptimizeLog, BeforeTokens: 2, AfterTokens: 1}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, time.Now().UTC().Format("2006-01-02")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"operation":"optimize_log"`) {
		t.Fatalf("entry not serialized as expected: %s", raw)
	}
}

func TestEntriesOrderedAndCapped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	for i := 0; i < 3; i++ {
		if err := r.Record(Entry{
			Time:      time.Now().Add(time.Duration(i) * time.Hour),
			Operation: OpOptimizePrompt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	es, err := r.Entries(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(es))
	}
	// Newest first.
	if es[0].Time.Before(es[1].Time) {
		t.Fatal("entries must be newest-first")
	}
}

func TestEntriesDefaultLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	es, err := r.Entries(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 0 {
		t.Fatalf("expected no entries, got %d", len(es))
	}
}

// TestSummarizeBoundaryDay verifies days=N covers exactly N calendar days
// including today: a file dated N days ago is excluded, N-1 days ago included
// (W2-34).
func TestSummarizeBoundaryDay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	writeDay := func(daysAgo int, ops int) {
		name := time.Now().UTC().AddDate(0, 0, -daysAgo).Format("2006-01-02") + ".jsonl"
		var b strings.Builder
		for i := 0; i < ops; i++ {
			b.WriteString(`{"operation":"optimize_prompt","before_tokens":2,"after_tokens":1,"saved_tokens":1,"cost_saved_usd":0}` + "\n")
		}
		_ = os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
	}
	writeDay(7, 3)
	writeDay(6, 2)
	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 2 {
		t.Fatalf("expected only the N-1-days-old entry in a 7-day window, got %d", sum.Operations)
	}
}
