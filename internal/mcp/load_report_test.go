package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	loadReportMu      sync.Mutex
	loadReportResults []loadResult
)

// recordLoadResult appends one measured load result and, when
// KERN_LOAD_REPORT_DIR is set, refreshes the JSON and Markdown reports on
// disk. Report writing is best-effort: a reporting failure must never turn a
// passing tool run into a test failure.
func recordLoadResult(result loadResult) {
	loadReportMu.Lock()
	defer loadReportMu.Unlock()

	loadReportResults = append(loadReportResults, result)
	sort.SliceStable(loadReportResults, func(i, j int) bool {
		if loadReportResults[i].Tool == loadReportResults[j].Tool {
			return loadReportResults[i].Profile < loadReportResults[j].Profile
		}
		return loadReportResults[i].Tool < loadReportResults[j].Tool
	})

	dir := os.Getenv("KERN_LOAD_REPORT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if data, err := json.MarshalIndent(loadReportResults, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "load.json"), data, 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "load.md"), []byte(renderLoadMarkdown(loadReportResults)), 0o644)
}

func resetLoadReport() {
	loadReportMu.Lock()
	defer loadReportMu.Unlock()
	loadReportResults = nil
}

func renderLoadMarkdown(results []loadResult) string {
	var b strings.Builder
	b.WriteString("# Kern load report\n\n")
	b.WriteString("| Tool | Profile | Persona | Requests | Concurrency | Successes | Errors | P50 | P95 | P99 | Max | Throughput | Cache H/M | RSS Start→End (Δ) | Served By |\n")
	b.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, r := range results {
		errors := r.ToolErrors + r.TransportErr
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d | %d | %d | %d | %s | %s | %s | %s | %.2f/s | %d/%d | %d→%d (%+d) | `%s` |\n",
			r.Tool, r.Profile, r.Persona, r.Requests, r.Concurrency, r.Successes, errors,
			r.P50, r.P95, r.P99, r.Max, r.Throughput, r.CacheHits, r.CacheMisses,
			r.RSSStart, r.RSSEnd, r.RSSDelta, r.ServedBy)
	}
	return b.String()
}

func TestLoadReportWritesJSONAndMarkdown(t *testing.T) {
	resetLoadReport()
	dir := t.TempDir()
	t.Setenv("KERN_LOAD_REPORT_DIR", dir)
	defer resetLoadReport()

	recordLoadResult(loadResult{
		Tool:        "kern_search",
		Profile:     "smoke",
		Persona:     personaDeveloper,
		Requests:    10,
		Concurrency: 2,
		Successes:   10,
		P50:         10 * time.Millisecond,
		P95:         20 * time.Millisecond,
		P99:         30 * time.Millisecond,
		Max:         35 * time.Millisecond,
		Throughput:  12.5,
		RSSStart:    10 << 20,
		RSSEnd:      11 << 20,
		RSSDelta:    1 << 20,
		CacheHits:   3,
		CacheMisses: 1,
		ServedBy:    "in_process",
	})

	jsonPath := filepath.Join(dir, "load.json")
	mdPath := filepath.Join(dir, "load.md")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read load.json: %v", err)
	}
	var decoded []loadResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode load.json: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Tool != "kern_search" {
		t.Fatalf("unexpected JSON report: %+v", decoded)
	}

	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read load.md: %v", err)
	}
	if !strings.Contains(string(md), "kern_search") || !strings.Contains(string(md), "P95") ||
		!strings.Contains(string(md), "Cache H/M") || !strings.Contains(string(md), "Served By") {
		t.Fatalf("markdown report missing expected columns:\n%s", md)
	}
}
