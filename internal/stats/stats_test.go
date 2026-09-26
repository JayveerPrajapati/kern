package stats

import (
	"encoding/json"
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
	if got := CostPerMillion("gpt-4o-2024-08-06"); got != 2.50 {
		t.Fatalf("versioned model must prefix-match the family rate, got %f", got)
	}
	if got := CostPerMillion("o3-mini"); got != 1.10 {
		t.Fatalf("o3-mini must resolve its own rate, not the o3 prefix 10.00, got %f", got)
	}
	if got := CostPerMillion("local"); got != 0 {
		t.Fatalf("local must be free, got %f", got)
	}
	// Unknown/empty models resolve to the flat assumed default (1e-05 $/token
	// = $10/1M), coherent with the aggregate "cost model" label.
	if got := CostPerMillion("unknown-model"); got != 10.00 {
		t.Fatalf("unknown model must fall to the flat assumed default 10.00, got %f", got)
	}
	if got := CostPerMillion(""); got != 10.00 {
		t.Fatalf("empty model must fall to the flat assumed default 10.00, got %f", got)
	}
}

func TestModelSavings(t *testing.T) {
	savings := ModelSavings(1_000_000)
	if savings["gpt-4o"] != 2.50 {
		t.Errorf("expected $2.50 savings on gpt-4o for 1M tokens, got: %f", savings["gpt-4o"])
	}
	if savings["claude-3-5-sonnet"] != 3.00 {
		t.Errorf("expected $3.00 savings on claude-3-5-sonnet for 1M tokens, got: %f", savings["claude-3-5-sonnet"])
	}
	zero := ModelSavings(0)
	if len(zero) != 0 {
		t.Errorf("expected empty map on 0 tokens, got %v", zero)
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
// including today: a file dated N days ago is excluded, N-1 days ago
// included.
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

func TestNewRecorderCreatesWorkingRecorder(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	r, err := NewRecorder()
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if r == nil || r.dir == "" {
		t.Fatalf("NewRecorder returned unusable recorder: %+v", r)
	}

	if err := r.Record(Entry{
		Session:      "test-session",
		Operation:    OpOptimizePrompt,
		Model:        "local",
		BeforeTokens: 100,
		AfterTokens:  75,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	sum, err := r.Summarize(7, "test-session")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Operations != 1 || sum.BeforeTotal != 100 || sum.AfterTotal != 75 {
		t.Fatalf("summary missing recorded entry: %+v", sum)
	}
	if sum.SavedTotal != 25 || sum.SavedPct != 25 {
		t.Fatalf("saved totals wrong: %+v", sum)
	}
}

func TestZeroValueRecorderSummarizeDoesNotPanic(t *testing.T) {
	var r Recorder // zero value: empty dir
	sum, err := r.Summarize(7, "")
	if err == nil {
		t.Fatal("Summarize on zero-value recorder with empty dir: want error, got nil")
	}
	if sum == nil || sum.Operations != 0 {
		t.Fatalf("want empty summary, got %+v", sum)
	}
}

// TestEntryBackwardCompat verifies that JSONL written before the Tool field
// existed reads back cleanly (Tool == "") and that a Tool-carrying entry
// round-trips. Existing stats files must stay readable.
func TestEntryBackwardCompat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Old-format line: no "tool" key at all.
	old := `{"time":"2026-09-25T10:00:00Z","session":"s-old","operation":"optimize_prompt","before_tokens":100,"after_tokens":40,"saved_tokens":60,"saved_percent":60,"cost_saved_usd":0}`
	// New-format line: carries a tool.
	new := `{"time":"2026-09-25T11:00:00Z","session":"s-new","operation":"tool_call","tool":"kern_search","after_tokens":120}`
	if err := os.WriteFile(filepath.Join(dir, "2026-09-25.jsonl"), []byte(old+"\n"+new+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	es, err := r.Entries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(es))
	}
	var oldE, newE Entry
	for _, e := range es {
		if e.Session == "s-old" {
			oldE = e
		} else {
			newE = e
		}
	}
	if oldE.Tool != "" {
		t.Fatalf("old-format entry must read Tool as empty, got %q", oldE.Tool)
	}
	if oldE.BeforeTokens != 100 || oldE.AfterTokens != 40 || oldE.SavedTokens != 60 {
		t.Fatalf("old-format entry fields mangled: %+v", oldE)
	}
	if newE.Tool != "kern_search" || newE.Operation != OpToolCall || newE.AfterTokens != 120 {
		t.Fatalf("new-format entry fields wrong: %+v", newE)
	}
}

// TestSummarizeSkipsToolCallEntries verifies the optimization-events ledger
// ignores per-tool dispatch entries (Operation == OpToolCall), so existing
// surfaces ("N ops, N tokens saved") keep their meaning.
func TestSummarizeSkipsToolCallEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	if err := r.Record(Entry{Time: time.Now(), Operation: OpOptimizePrompt, BeforeTokens: 100, AfterTokens: 40}); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(Entry{Time: time.Now(), Operation: OpToolCall, Tool: "kern_search", AfterTokens: 500}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.Summarize(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 1 {
		t.Fatalf("tool_call entries must not count as optimization ops, got %d", sum.Operations)
	}
	if sum.AfterTotal != 40 {
		t.Fatalf("tool_call payload must not leak into the summary, after=%d", sum.AfterTotal)
	}
	if sum.ByOperation[OpToolCall] != 0 {
		t.Fatalf("OpToolCall must not appear in by-operation, got %v", sum.ByOperation)
	}
}

// TestRecordZeroBeforeNoFabricatedSavings verifies a per-tool dispatch entry
// (Before=0, After>0) records saved_tokens=0 — never negative fabricated
// savings.
func TestRecordZeroBeforeNoFabricatedSavings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	if err := r.Record(Entry{Operation: OpToolCall, Tool: "kern_graph", AfterTokens: 200}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, time.Now().UTC().Format("2006-01-02")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.SavedTokens != 0 || e.CostSavedUSD != 0 {
		t.Fatalf("zero-before entry must record zero savings, got %+v", e)
	}
}

// TestSummarizeByTool verifies the per-tool aggregation math: calls, tokens
// returned, tokens saved and cost saved, ignoring entries without a Tool, and
// sorted by savings desc (then returned desc).
func TestSummarizeByTool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	// Two dispatch entries for kern_search (After only, no savings).
	_ = r.Record(Entry{Operation: OpToolCall, Tool: "kern_search", AfterTokens: 100})
	_ = r.Record(Entry{Operation: OpToolCall, Tool: "kern_search", AfterTokens: 50})
	// Two optimization entries for kern_optimize_prompt (real savings).
	_ = r.Record(Entry{Operation: OpOptimizePrompt, Tool: "kern_optimize_prompt", Model: "gpt-4o", BeforeTokens: 1000, AfterTokens: 400})
	_ = r.Record(Entry{Operation: OpOptimizePrompt, Tool: "kern_optimize_prompt", Model: "gpt-4o", BeforeTokens: 100, AfterTokens: 50})
	// A big returned payload for kern_graph.
	_ = r.Record(Entry{Operation: OpToolCall, Tool: "kern_graph", AfterTokens: 3000})
	// An optimization entry WITHOUT a tool must be ignored by by-tool.
	_ = r.Record(Entry{Operation: OpRunBuild, BeforeTokens: 10, AfterTokens: 5})

	tools, err := r.SummarizeByTool(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %+v", len(tools), tools)
	}
	// Sorted by saved desc: kern_optimize_prompt (650) first, then the two
	// zero-savings tools by returned desc (kern_graph 3000 > kern_search 150).
	if tools[0].Tool != "kern_optimize_prompt" || tools[1].Tool != "kern_graph" || tools[2].Tool != "kern_search" {
		t.Fatalf("sort order wrong: %+v", tools)
	}
	opt := tools[0]
	if opt.Calls != 2 || opt.TokensReturned != 450 || opt.TokensSaved != 650 {
		t.Fatalf("optimize_prompt aggregation wrong: %+v", opt)
	}
	// 650 saved tokens at $2.50/1M = $0.001625.
	if opt.CostSaved < 0.0016 || opt.CostSaved > 0.00165 {
		t.Fatalf("optimize_prompt cost wrong: %f", opt.CostSaved)
	}
	graph := tools[1]
	if graph.Calls != 1 || graph.TokensReturned != 3000 || graph.TokensSaved != 0 || graph.CostSaved != 0 {
		t.Fatalf("graph aggregation wrong: %+v", graph)
	}
	search := tools[2]
	if search.Calls != 2 || search.TokensReturned != 150 || search.TokensSaved != 0 {
		t.Fatalf("search aggregation wrong: %+v", search)
	}
}

// TestSummarizeByAgent verifies the per-agent aggregation math: entries are
// grouped by Agent, entries without an attribution fall into the
// "(unattributed)" bucket, and rows sort by savings desc (then returned
// desc, then name).
func TestSummarizeByAgent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	// Two dispatch entries for agent "planner" (After only, no savings).
	_ = r.Record(Entry{Operation: OpToolCall, Tool: "kern_search", Agent: "planner", AfterTokens: 100})
	_ = r.Record(Entry{Operation: OpToolCall, Tool: "kern_graph", Agent: "planner", AfterTokens: 3000})
	// One optimization entry for agent "coder" (real savings).
	_ = r.Record(Entry{Operation: OpOptimizePrompt, Tool: "kern_optimize_prompt", Agent: "coder", Model: "gpt-4o", BeforeTokens: 1000, AfterTokens: 400})
	// Entries without an Agent fall into the (unattributed) bucket.
	_ = r.Record(Entry{Operation: OpOptimizePrompt, Model: "gpt-4o", BeforeTokens: 100, AfterTokens: 50})

	agents, err := r.SummarizeByAgent(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d: %+v", len(agents), agents)
	}
	// Sorted by saved desc: coder (600) first, then (unattributed) (50),
	// then planner (0 saved, 3100 returned).
	if agents[0].Agent != "coder" || agents[1].Agent != unattributedAgent || agents[2].Agent != "planner" {
		t.Fatalf("sort order wrong: %+v", agents)
	}
	coder := agents[0]
	if coder.Calls != 1 || coder.TokensReturned != 400 || coder.TokensSaved != 600 {
		t.Fatalf("coder aggregation wrong: %+v", coder)
	}
	// 600 saved tokens at $2.50/1M = $0.0015.
	if coder.CostSaved < 0.0014 || coder.CostSaved > 0.0016 {
		t.Fatalf("coder cost wrong: %f", coder.CostSaved)
	}
	planner := agents[2]
	if planner.Calls != 2 || planner.TokensReturned != 3100 || planner.TokensSaved != 0 {
		t.Fatalf("planner aggregation wrong: %+v", planner)
	}
	unatt := agents[1]
	if unatt.Calls != 1 || unatt.TokensReturned != 50 || unatt.TokensSaved != 50 {
		t.Fatalf("unattributed aggregation wrong: %+v", unatt)
	}
}

// TestSummarizeByAgentDayWindow verifies the day filter applies to the
// per-agent ledger: entries in stale files (outside the window) contribute
// nothing.
func TestSummarizeByAgentDayWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	oldDay := filepath.Join(dir, time.Now().AddDate(0, 0, -10).Format("2006-01-02")+".jsonl")
	_ = os.WriteFile(oldDay, []byte(`{"operation":"tool_call","tool":"kern_search","agent":"planner","after_tokens":100}`+"\n"), 0o644)
	agents, err := r.SummarizeByAgent(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("stale entries must be excluded from the 7-day window, got %+v", agents)
	}
}
