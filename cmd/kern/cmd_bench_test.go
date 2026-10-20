package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// collectJSONTags gathers every json tag name declared by t, recursing into
// nested struct and []struct fields so the whole benchResult schema is
// compared, not just the top level.
func collectJSONTags(t reflect.Type, tags map[string]bool) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			continue
		}
		tags[name] = true
		switch f.Type.Kind() {
		case reflect.Struct:
			collectJSONTags(f.Type, tags)
		case reflect.Slice:
			if f.Type.Elem().Kind() == reflect.Struct {
				collectJSONTags(f.Type.Elem(), tags)
			}
		}
	}
}

// collectFixtureKeys gathers every JSON object key at every nesting level of
// the decoded fixture document (objects and arrays of objects).
func collectFixtureKeys(v any, keys map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			keys[k] = true
			collectFixtureKeys(sub, keys)
		}
	case []any:
		for _, e := range t {
			collectFixtureKeys(e, keys)
		}
	}
}

// TestBenchResultRoundTripGoldenFixture pins the WRITE side of
// .kern/bench.json — cmd/kern's benchResult — against the golden fixture the
// web console's decoder already pins (internal/web/testdata/bench.json, see
// internal/web/bench_schema_test.go's TestBenchReportGoldenFixture). The two
// structs are hand-duplicated; the web test only pins the DECODE side, so a
// field renamed here would pass the web test and silently zero the
// /benchmarks page. This test asserts the fixture's JSON keys EXACTLY match
// benchResult's json tags, in both directions, at every nesting level (audit
// iteration-3 finding 3).
func TestBenchResultRoundTripGoldenFixture(t *testing.T) {
	fixturePath := "testdata/bench.json" // test cwd is the package dir (cmd/kern); fixture lives in testdata
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", fixturePath, err)
	}

	// The fixture must decode into the write-side schema; a rename here
	// already fails loudly at this unmarshal.
	var res benchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode %s into benchResult: %v", fixturePath, err)
	}

	// Schema contract: fixture keys (have) must exactly match benchResult's
	// json tags (want).
	want := map[string]bool{}
	collectJSONTags(reflect.TypeOf(benchResult{}), want)

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s document: %v", fixturePath, err)
	}
	have := map[string]bool{}
	collectFixtureKeys(doc, have)

	for k := range want {
		if !have[k] {
			t.Errorf("fixture %s is missing json key %q declared by benchResult (cmd/kern/cmd_bench.go) — update the fixture in lockstep", fixturePath, k)
		}
	}
	for k := range have {
		if !want[k] {
			t.Errorf("benchResult (cmd/kern/cmd_bench.go) has no json tag %q present in fixture %s — rename the field in lockstep with internal/web/builders.go and the fixture", k, fixturePath)
		}
	}
}

// TestFmtLatencyNeverShowsZero pins the dogfooding B-LOW display fix: a
// sub-millisecond query latency must never render as "0.00 ms" — that reads
// as "the query returned nothing" (the one-hop-callers row really returned
// 3830 callers in ~40ns). Three tiers: ms, µs, ns.
func TestFmtLatencyNeverShowsZero(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{250.5, " 250.50 ms"},
		{1.0, "   1.00 ms"},
		{0.5, "  500.0 µs"},
		{0.001, "    1.0 µs"},
		{0.0005, "    500 ns"},
		{0.00004, "     40 ns"},
	}
	for _, c := range cases {
		if got := fmtLatency(c.ms); got != c.want {
			t.Errorf("fmtLatency(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
	// A zero-valued sample (no runs) must still render as ns, never "0.00 ms".
	if got := fmtLatency(0); strings.Contains(got, "ms") {
		t.Errorf("fmtLatency(0) = %q — a zero sample must not masquerade as 0.00 ms", got)
	}
}

// TestBenchCommandDispatches pins the bench registration the dispatch table
// must carry: runBench existed with NO commandTable entry, so `kern bench`
// exited 2 "unknown command" — breaking the documented `make bench-latency`
// regen command (Makefile) and the README's live-harness claim. It runs the
// command END-TO-END on a tiny fixture: dispatchCommand must resolve "bench"
// and the harness must produce its canonical outputs — the human latency
// table on stdout AND <root>/.kern/bench.json (the web console's /benchmarks
// data file).
func TestBenchCommandDispatches(t *testing.T) {
	dir := jsonCliFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("bench", []string{"--root", dir}); code != 0 {
			t.Errorf("dispatchCommand(bench) = %d, want 0 (bench must resolve; an unregistered command exits 2)", code)
		}
	})
	if !strings.Contains(out, "kern bench — deterministic latency harness") {
		t.Errorf("bench stdout is missing the harness header, got: %.200s", out)
	}
	for _, row := range []string{"cold build (fresh)", "warm load (store)", "blast"} {
		if !strings.Contains(out, row) {
			t.Errorf("bench table is missing the %q row, got: %.300s", row, out)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".kern", "bench.json"))
	if err != nil {
		t.Fatalf("bench must write .kern/bench.json in the fixture root: %v", err)
	}
	var res benchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode .kern/bench.json into benchResult: %v", err)
	}
	if res.Suite != "kern-bench" || len(res.Queries) == 0 {
		t.Errorf("bench.json carries the wrong envelope: suite=%q queries=%d", res.Suite, len(res.Queries))
	}
	for _, q := range res.Queries {
		if q.Results == 0 {
			t.Errorf("query %s recorded 0 results — the count capture regressed (the --check zero-result gate depends on it)", q.Name)
		}
	}
}

// TestBenchCheckThresholds table-pins the pure gate logic of `kern bench
// --check` (perfection plan B1): ceilings fail loud, the warn factor warns
// without failing, the cold/warm speedup floor guards the persisted-store
// fast path, zero-result queries are wiring failures (not latency), and a
// ceiling of 0 disables its check.
func TestBenchCheckThresholds(t *testing.T) {
	base := benchResult{
		WarmLoadMS: sample{MedianMS: 100, MinMS: 90, Runs: 3},
		ColdVsWarm: 8.0,
		Queries: []queryResult{
			{Name: "search", MedianMS: 0.5, Results: 20},
			{Name: "blast", MedianMS: 5, Results: 137},
		},
	}
	th := benchThresholds{WarmLoadMSMax: 2000, QueryMSMax: 1000, ColdVsWarmMin: 2, WarnFactor: 0.5, MinResults: 1}

	if v := checkBenchThresholds(base, th); len(v) != 0 {
		t.Fatalf("healthy run must be clean, got %+v", v)
	}

	// Warm-load ceiling exceeded → fail.
	bad := base
	bad.WarmLoadMS = sample{MedianMS: 2500, Runs: 3}
	v := checkBenchThresholds(bad, th)
	if len(v) != 1 || v[0].Level != "fail" || v[0].Check != "warm_load_ms" {
		t.Fatalf("warm-load ceiling must fail, got %+v", v)
	}

	// A query past the warn factor but under the ceiling → warn only.
	bad = base
	bad.Queries = []queryResult{{Name: "hubs", MedianMS: 600, Results: 9}} // 600 > 500 (50% of 1000)
	v = checkBenchThresholds(bad, th)
	if len(v) != 1 || v[0].Level != "warn" {
		t.Fatalf("past-warn-factor query must warn (not fail), got %+v", v)
	}

	// Speedup floor violated → fail with the fall-through diagnosis.
	bad = base
	bad.ColdVsWarm = 1.2
	v = checkBenchThresholds(bad, th)
	if len(v) != 1 || v[0].Level != "fail" || v[0].Check != "cold_vs_warm_speedup" || !strings.Contains(v[0].Detail, "falling through") {
		t.Fatalf("speedup-floor violation must fail with the diagnosis, got %+v", v)
	}

	// Zero-result query → fail, framed as a wiring regression.
	bad = base
	bad.Queries = []queryResult{{Name: "ranked", MedianMS: 0.5, Results: 0}}
	v = checkBenchThresholds(bad, th)
	if len(v) != 1 || v[0].Level != "fail" || v[0].Check != "ranked_results" || !strings.Contains(v[0].Detail, "wiring") {
		t.Fatalf("zero-result query must fail as a wiring regression, got %+v", v)
	}

	// min_results = 0 disables the zero-result check.
	thNoMin := th
	thNoMin.MinResults = 0
	if v := checkBenchThresholds(bad, thNoMin); len(v) != 0 {
		t.Fatalf("min_results=0 must disable the zero-result check, got %+v", v)
	}

	// A ceiling of 0 disables that check entirely (0 = unmeasured value too).
	thOff := benchThresholds{ColdVsWarmMin: 2}
	if v := checkBenchThresholds(base, thOff); len(v) != 0 {
		t.Fatalf("zero ceilings must disable their checks, got %+v", v)
	}
}

// TestBenchThresholdsEmbeddedFileParses pins the versioned gate config
// itself: the embedded bench_thresholds.json must decode with every
// threshold set to a sane positive value — a hand-edit that zeroes or drops
// a field would silently disable that check (checkBenchThresholds treats 0
// as disabled), so the file fails loud here instead.
func TestBenchThresholdsEmbeddedFileParses(t *testing.T) {
	th := loadBenchThresholds()
	if th.WarmLoadMSMax <= 0 || th.QueryMSMax <= 0 || th.ColdVsWarmMin <= 0 || th.WarnFactor <= 0 || th.WarnFactor >= 1 || th.MinResults < 1 {
		t.Fatalf("embedded bench_thresholds.json is missing or disables a check: %+v", th)
	}
}

// TestBenchmarkDocsCarryStatusLabels is the label-scheme honesty gate for
// docs/benchmarks/: the repo markets honest evidence labels ([measured] /
// [synthetic] / [historical] / [superseded] / [illustrative] — see
// README.md's "evidence labels" claim), so every page under docs/benchmarks/
// must declare one on its status line (first 12 lines), machine-checkable.
// A page added without a label, or a label edit that drifts off the status
// line, fails here. The suite index (README.md) is exempt: it is the index
// page and carries the label vocabulary on its own status line.
func TestBenchmarkDocsCarryStatusLabels(t *testing.T) {
	labels := []string{"[measured]", "[synthetic]", "[historical]", "[superseded]", "[illustrative]"}
	haveLabel := func(line string) bool {
		for _, l := range labels {
			if strings.Contains(line, l) {
				return true
			}
		}
		return false
	}
	pages := []string{
		"accuracy.md",
		"agent-evals.md",
		"cold-start.md",
		"command-qa-ledger.md",
		"graph-latency.md",
		"multi-repo-latency.md",
		"README.md",
		"semantic-retention.md",
		"telemetry-audit.md",
		"token-savings.md",
		"fixtures/README.md",
	}
	checked := 0
	for _, rel := range pages {
		path := filepath.Join("..", "..", "docs", "benchmarks", rel)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.SplitN(string(b), "\n", 12)
		labeled := false
		for _, line := range lines {
			if haveLabel(line) {
				labeled = true
				break
			}
		}
		checked++
		if !labeled {
			t.Errorf("docs/benchmarks/%s has no status label (%s) on its status line — add one of %v to the page's **Status:** line", rel, strings.Join(labels, "|"), labels)
		}
	}
	if checked != len(pages) {
		t.Fatalf("checked %d pages, want %d — update the page list in this test when docs/benchmarks/ grows", checked, len(pages))
	}
}

// TestMultiRepoBenchDocFresh pins docs/benchmarks/multi-repo-latency.md to
// the publication contract for MEASURED numbers: the numbers themselves are
// machine-bound and only meaningful against the disclosed environment, so
// this gate enforces the structure that keeps them honest — every data row
// fully columned and provenance-tagged, the regen command present, the
// environment (binary revision + machine) disclosed, and the suite indexed
// from docs/benchmarks/README.md. A table edited to drop a column, drop the
// [measured] tag, or ship without its environment disclosure fails here.
func TestMultiRepoBenchDocFresh(t *testing.T) {
	b, err := os.ReadFile("../../docs/benchmarks/multi-repo-latency.md")
	if err != nil {
		t.Fatalf("read docs/benchmarks/multi-repo-latency.md: %v", err)
	}
	doc := string(b)

	// Regeneration command must be documented.
	if !strings.Contains(doc, "kern bench --root <repo> --json") {
		t.Error("page must document the exact regen command (`kern bench --root <repo> --json`)")
	}
	// Environment disclosure: revision and machine shape.
	if !strings.Contains(doc, "Revision:") || !strings.Contains(doc, "darwin/arm64") {
		t.Error("page must disclose the binary revision and the measurement machine")
	}
	// Every measured data row: table row with 11 columns (header + 10 data
	// columns) whose first cell carries the repo label, and every row must
	// sit under the [measured]-tagged results section.
	rows := 0
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| repo") ||
			strings.HasPrefix(line, "|---") {
			continue
		}
		cols := strings.Count(line, "|") - 1
		if cols != 11 {
			t.Errorf("measured data row must have 11 columns (repo..hubs), got %d: %q", cols, line)
		}
		rows++
	}
	if rows < 4 {
		t.Errorf("page must carry at least the 4 measured repo rows, found %d", rows)
	}
	if !strings.Contains(doc, "**Provenance:** [measured]") {
		t.Error("page must carry the [measured] provenance label on the results")
	}
	// The findings section must cross-reference its regression tests by
	// name so a rename shows up as a doc drift here.
	for _, pin := range []string{
		"TestTreeSitterAnonymousArrowParamsNotSymbols",
		"TestTreeSitterCSSReselectionDeduped",
		"TestCssReselectionDedupedRegex",
	} {
		if !strings.Contains(doc, pin) {
			t.Errorf("page must cross-reference regression test %s", pin)
		}
	}
	// The suite index must link the page.
	idx, err := os.ReadFile("../../docs/benchmarks/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "multi-repo-latency.md") {
		t.Error("docs/benchmarks/README.md must index multi-repo-latency.md in its suites table")
	}
}
