package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestBenchReportGoldenFixture pins the .kern/bench.json schema written by
// `kern bench` (cmd/kern/cmd_bench.go's benchResult) against the web
// console's decoder (benchReport in builders.go). The two structs are
// hand-duplicated: a renamed field would silently zero the /benchmarks page,
// so this golden fixture (internal/web/testdata/bench.json, deterministic —
// hand-written, no machine-timing numbers) is the lockstep contract. Change
// benchResult or benchReport ONLY in lockstep with the fixture (audit
// iteration-2 finding 5).
func TestBenchReportGoldenFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/bench.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var rep benchReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("decode fixture into benchReport: %v", err)
	}
	if rep.Suite != "kern-bench" {
		t.Errorf("suite = %q, want kern-bench", rep.Suite)
	}
	if rep.Date != "2026-09-25T00:00:00Z" {
		t.Errorf("date = %q", rep.Date)
	}
	if rep.GitHead != "0000000" {
		t.Errorf("git_head = %q", rep.GitHead)
	}
	if rep.SymbolCount != 42 {
		t.Errorf("symbol_count = %d, want 42", rep.SymbolCount)
	}
	if rep.Machine.GOOS != "darwin" || rep.Machine.GOARCH != "arm64" || rep.Machine.NumCPU != 8 {
		t.Errorf("machine = %+v", rep.Machine)
	}
	if rep.ColdLoadMS.MedianMS != 1234.5 || rep.ColdLoadMS.MinMS != 1234.5 || rep.ColdLoadMS.Runs != 1 {
		t.Errorf("cold_load_ms = %+v", rep.ColdLoadMS)
	}
	if rep.WarmLoadMS.MedianMS != 12.345 || rep.WarmLoadMS.Runs != 3 {
		t.Errorf("warm_load_ms = %+v", rep.WarmLoadMS)
	}
	if rep.ColdVsWarm != 100.0 {
		t.Errorf("cold_vs_warm_speedup = %v", rep.ColdVsWarm)
	}
	if len(rep.Queries) != 2 {
		t.Fatalf("queries = %d, want 2", len(rep.Queries))
	}
	q := rep.Queries[0]
	if q.Name != "search" || q.Kind != "symbol-search" || q.Target != "main" || q.MedianMS != 1.2 || q.MinMS != 0.9 || q.Runs != 5 {
		t.Errorf("queries[0] = %+v", q)
	}
	if rep.Queries[1].Name != "blast" || rep.Queries[1].Target != "Public" || rep.Queries[1].Runs != 5 {
		t.Errorf("queries[1] = %+v", rep.Queries[1])
	}
	if !strings.Contains(rep.MethodologyRef, "graph-latency") {
		t.Errorf("methodology_ref = %q", rep.MethodologyRef)
	}
	if rep.Root != "/tmp/kern-bench-fixture" {
		t.Errorf("root = %q", rep.Root)
	}
}
