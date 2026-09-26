package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// runBench implements `kern bench`: a deterministic, zero-network latency
// harness for this repo. It measures the two load paths every kern command
// pays — cold index build vs warm persisted-store load — plus a small fixed
// set of query latencies (symbol search, ranked search, one-hop callers,
// transitive blast radius, hub ranking), mirroring the G12 latency-test
// methodology in internal/bpreceipt/metrics (cold-vs-warm p50 recording) and
// the docs/benchmarks/graph-latency.md suite (median/min over repeated runs,
// index build excluded from query timings).
//
// Output: a human table on stdout AND a JSON document at <root>/.kern/bench.json
// (the canonical path the web console's /benchmarks page reads). No network,
// no LLM calls, no external tools — pure stdlib + the existing index/intel
// query paths.
func runBench(rest []string) {
	f, _ := parseFlagsOrDie(rest)
	root := projectRoot(f)

	// ---- Cold load: full index build from source (no persisted store). ----
	// Single run: builds are the expensive path (seconds on real repos) and
	// graph-latency.md §4 records the cold build the same way — one honest
	// wall-time sample, not a noise-averaged distribution.
	buildStart := time.Now()
	ix, err := index.Build(root)
	if err != nil {
		fatal("Bench: build index: %v", err)
	}
	cold := time.Since(buildStart)

	// Persist the freshly built index so the warm-load runs below read a
	// real store (Load with nothing on disk would fall through to Build and
	// silently measure the cold path twice).
	if err := ix.Save(); err != nil {
		fatal("Bench: save index: %v", err)
	}

	// ---- Warm load: persisted-store load (what every command pays). ----
	warm := measure(3, func() { _, _ = index.Load(root) })

	// ---- Query latencies (warm in-memory index, mirroring graph-latency.md
	// query kinds). Graph queries target the index's top-hub symbol — the
	// "Public" equivalent of the graph-latency fixture — resolved
	// deterministically from the index itself (most callers), falling back to
	// "main" on an empty index.
	hub := topHub(ix)
	queries := []struct {
		name   string
		kind   string
		target string
		run    func()
	}{
		{"search", "symbol-search", "main", func() { ix.Search("main", 20) }},
		{"ranked", "ranked-search", "main", func() { intel.RankedSearch(ix, "main", 20) }},
		{"callers", "one-hop-callers", hub, func() { ix.CallersOf(hub) }},
		{"blast", "transitive-blast-radius", hub, func() { intel.BlastRadius(ix, []string{hub}) }},
		{"hubs", "hub-ranking", "", func() { intel.Hubs(ix, 10) }},
	}
	queryResults := make([]queryResult, 0, len(queries))
	for _, q := range queries {
		// Warm-up once so lazy maps/tables (e.g. buildSymbolIndex side
		// effects on first query) are outside the timed runs.
		q.run()
		s := measure(5, q.run)
		queryResults = append(queryResults, queryResult{
			Name:     q.name,
			Kind:     q.kind,
			Target:   q.target,
			MedianMS: s.MedianMS,
			MinMS:    s.MinMS,
			Runs:     s.Runs,
		})
	}

	// ---- Machine context (honest-reporting envelope). ----
	res := benchResult{
		Suite:          "kern-bench",
		Date:           time.Now().Format(time.RFC3339),
		Root:           root,
		GitHead:        shortHash(),
		SymbolCount:    len(ix.Symbols),
		Machine:        machineContext(),
		ColdLoadMS:     sample{MedianMS: ms(cold), MinMS: ms(cold), Runs: 1},
		WarmLoadMS:     warm,
		ColdVsWarm:     speedup(cold, warm.MedianMS),
		Queries:        queryResults,
		MethodologyRef: "docs/benchmarks/graph-latency.md + internal/bpreceipt/metrics G12 (cold-vs-warm, median/min over repeated runs)",
	}

	// ---- Persist the canonical JSON the web page reads. ----
	kernDir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		fatal("Bench: mkdir %s: %v", kernDir, err)
	}
	outPath := filepath.Join(kernDir, "bench.json")
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal("Bench: encode: %v", err)
	}
	if err := os.WriteFile(outPath, append(raw, '\n'), 0o644); err != nil {
		fatal("Bench: write %s: %v", outPath, err)
	}

	// ---- Human table. ----
	printBenchTable(res)
	if f.json {
		printJSON(res)
	}
	fmt.Printf("wrote %s (%d symbols, %d queries)\n", outPath, res.SymbolCount, len(res.Queries))
}

// benchResult is the schema of .kern/bench.json — the document the web
// console's /benchmarks page renders. Field names are the page's contract;
// change them in lockstep with internal/web/builders.go's benchReport AND
// the golden fixture internal/web/testdata/bench.json (pinned by
// internal/web/bench_schema_test.go's TestBenchReportGoldenFixture — a
// renamed field here silently zeroes the /benchmarks page).
type benchResult struct {
	Suite          string        `json:"suite"`
	Date           string        `json:"date"`
	Root           string        `json:"root"`
	GitHead        string        `json:"git_head"`
	SymbolCount    int           `json:"symbol_count"`
	Machine        machineInfo   `json:"machine"`
	ColdLoadMS     sample        `json:"cold_load_ms"`
	WarmLoadMS     sample        `json:"warm_load_ms"`
	ColdVsWarm     float64       `json:"cold_vs_warm_speedup"`
	Queries        []queryResult `json:"queries"`
	MethodologyRef string        `json:"methodology_ref"`
}

// sample is a latency distribution summary: median and min across runs,
// in milliseconds.
type sample struct {
	MedianMS float64 `json:"median_ms"`
	MinMS    float64 `json:"min_ms"`
	Runs     int     `json:"runs"`
}

// queryResult is one fixed query's latency summary plus its target (the
// symbol the graph queries resolved; empty for repo-wide scans).
type queryResult struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Target   string  `json:"target,omitempty"`
	MedianMS float64 `json:"median_ms"`
	MinMS    float64 `json:"min_ms"`
	Runs     int     `json:"runs"`
}

// machineInfo records the environment the numbers were measured on, so the
// published table can honestly qualify the context (same posture as
// graph-latency.md's "Environment of this baseline" section).
type machineInfo struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	NumCPU    int    `json:"num_cpu"`
}

func machineContext() machineInfo {
	return machineInfo{
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		NumCPU:    runtime.NumCPU(),
	}
}

// timedSample collects one run of fn's duration.
// measure runs fn n times and returns the median/min in ms plus run count.
func measure(n int, fn func()) sample {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		fn()
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return sample{
		MedianMS: ms(samples[len(samples)/2]),
		MinMS:    ms(samples[0]),
		Runs:     n,
	}
}

// ms converts a duration to milliseconds (float, 3 decimals).
func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// speedup returns cold/warm as a ratio (how many times faster the warm path
// is), guarding against a zero warm sample.
func speedup(cold time.Duration, warmMS float64) float64 {
	if warmMS <= 0 {
		return 0
	}
	return ms(cold) / warmMS
}

// topHub returns the symbol with the most callers in the index — the
// deterministic "hub" target for the graph queries (the "Public" analogue of
// the graph-latency fixture). Falls back to "main" for an empty index.
func topHub(ix *index.Index) string {
	best := ""
	bestN := -1
	for sym, callers := range ix.Callers {
		if len(callers) > bestN {
			best = sym
			bestN = len(callers)
		}
	}
	if best == "" {
		return "main"
	}
	return best
}

// printBenchTable renders the human-readable latency table.
func printBenchTable(res benchResult) {
	fmt.Println("kern bench — deterministic latency harness (zero network)")
	fmt.Println("suite:", res.Suite, "· date:", res.Date, "· git:", res.GitHead)
	fmt.Printf("machine: %s/%s %s (%d cpus) · symbols: %d · root: %s\n",
		res.Machine.GOOS, res.Machine.GOARCH, res.Machine.GoVersion, res.Machine.NumCPU, res.SymbolCount, res.Root)
	fmt.Println()
	fmt.Println("load path           median    min     runs")
	fmt.Printf("cold build (fresh)  %8.2f ms %8.2f ms %5d\n", res.ColdLoadMS.MedianMS, res.ColdLoadMS.MinMS, res.ColdLoadMS.Runs)
	fmt.Printf("warm load (store)   %8.2f ms %8.2f ms %5d\n", res.WarmLoadMS.MedianMS, res.WarmLoadMS.MinMS, res.WarmLoadMS.Runs)
	fmt.Printf("cold/warm speedup   %8.1fx\n", res.ColdVsWarm)
	fmt.Println()
	fmt.Println("query                kind                    target   median    min     runs")
	for _, q := range res.Queries {
		fmt.Printf("%-20s %-22s %-8s %7.2f ms %7.2f ms %5d\n", q.Name, q.Kind, q.Target, q.MedianMS, q.MinMS, q.Runs)
	}
	fmt.Println()
	fmt.Println("methodology: " + res.MethodologyRef)
}
