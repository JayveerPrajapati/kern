package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordNormalizesRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	rel := t.TempDir()
	if err := r.Record(Entry{
		Time:      time.Now(),
		Operation: OpOptimizePrompt,
		Root:      rel,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := r.Entries(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Root != filepath.Clean(abs) {
		t.Fatalf("root not normalized: got %q want %q", entries[0].Root, filepath.Clean(abs))
	}
}

func TestSummarizeByRootScopesExactProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	here := t.TempDir()
	other := t.TempDir()
	for _, root := range []string{here, other} {
		if err := r.Record(Entry{
			Time:         time.Now(),
			Operation:    OpOptimizePrompt,
			Root:         root,
			BeforeTokens: 100,
			AfterTokens:  50,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Record(Entry{
		Time:      time.Now(),
		Operation: OpOptimizePrompt,
	}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.SummarizeByRoot(7, "", here)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Operations != 1 || sum.SavedTotal != 50 {
		t.Fatalf("project summary leaked entries: %+v", sum)
	}
}

func TestRootsAndRootScopedToolUsage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	here, other := t.TempDir(), t.TempDir()
	entries := []Entry{
		{Time: time.Now(), Operation: OpToolCall, Tool: "kern_search", Root: here, AfterTokens: 100},
		{Time: time.Now(), Operation: OpToolCall, Tool: "kern_search", Root: here, AfterTokens: 50},
		{Time: time.Now(), Operation: OpToolCall, Tool: "kern_buddy", Root: other, AfterTokens: 10},
		{Time: time.Now(), Operation: OpToolCall, Tool: "kern_rootless", AfterTokens: 5},
	}
	for _, e := range entries {
		if err := r.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	roots, err := r.Roots(7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != here || roots[1] != other {
		t.Fatalf("roots = %v, want only the two sorted rooted projects", roots)
	}
	tools, err := r.SummarizeByTool(7, "", here)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Tool != "kern_search" || tools[0].Calls != 2 || tools[0].TokensReturned != 150 {
		t.Fatalf("root-scoped tool usage = %+v, want kern_search with 2 calls/150 tokens", tools)
	}
}

func TestSummarizeByRootCountsToolCalls(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{dir: dir}
	root := t.TempDir()
	if err := r.Record(Entry{
		Time:        time.Now(),
		Operation:   OpToolCall,
		Tool:        "kern_buddy",
		Root:        root,
		AfterTokens: 12,
	}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.SummarizeByRoot(7, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ToolCalls != 1 || sum.Operations != 0 {
		t.Fatalf("tool-call summary wrong: %+v", sum)
	}
}
