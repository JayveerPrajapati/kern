package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/topology"
)

// runBench implements `kern bench`: a deterministic, zero-network latency
// harness for this repo. It measures the two load paths every kern command
// pays — cold index build vs warm persisted-store load — plus a small fixed
// set of query latencies (symbol search, ranked search, one-hop callers,
// transitive blast radius, hub ranking), mirroring the cold-vs-warm p50
// methodology documented in docs/benchmarks/graph-latency.md (median/min
// over repeated runs, index build excluded from query timings).
//
// Output: a human table on stdout AND a JSON document at <root>/.kern/bench.json
// (the canonical path the web console's /benchmarks page reads). No network,
// no LLM calls, no external tools — pure stdlib + the existing index/intel
// query paths.
//
// With --check the run is additionally verified against the versioned
// thresholds in bench_thresholds.json (embedded; KERN_BENCH_THRESHOLDS
// overrides the path) and exits 1 on hard violations — the regression gate
// behind `make bench-gate`.
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
		run    func() int
	}{
		{"search", "symbol-search", "main", func() int { return len(ix.Search("main", 20)) }},
		{"ranked", "ranked-search", "main", func() int { return len(intel.RankedSearch(ix, "main", 20)) }},
		{"callers", "one-hop-callers", hub, func() int { return len(ix.CallersOf(hub)) }},
		{"blast", "transitive-blast-radius", hub, func() int {
			r, _ := intel.BlastRadius(ix, []string{hub})
			return len(r)
		}},
		{"hubs", "hub-ranking", "", func() int { return len(topology.Hubs(ix, 10)) }},
	}
	queryResults := make([]queryResult, 0, len(queries))
	for _, q := range queries {
		// Warm-up once so lazy maps/tables (e.g. buildSymbolIndex side
		// effects on first query) are outside the timed runs. The warm-up
		// run also captures the query's result count — the zero-result
		// gate in `--check` reads it (a query that returns nothing is a
		// wiring regression, not a latency finding).
		n := q.run()
		s := measure(5, func() { q.run() })
		queryResults = append(queryResults, queryResult{
			Name:     q.name,
			Kind:     q.kind,
			Target:   q.target,
			MedianMS: s.MedianMS,
			MinMS:    s.MinMS,
			Runs:     s.Runs,
			Results:  n,
		})
	}

	// ---- Fresh-process CLI latencies: one subprocess per run, so these include
	// process start + index load (the cost an agent's shell pays), not just the
	// warm in-memory query above.
	cliResults := measureCLI(root, cliSymbol(ix), heavyTypeSymbol(ix))

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
		CLIQueries:     cliResults,
		MethodologyRef: "docs/benchmarks/graph-latency.md (cold-vs-warm, median/min over repeated runs)",
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
	// ---- Optional regression gate (perfection plan B1): verify the run
	// against the versioned thresholds and exit 1 on hard violations
	// (findings tier, same as kern verify FAIL). ----
	if f.check {
		th := loadBenchThresholds()
		violations := checkBenchThresholds(res, th)
		fmt.Println()
		fmt.Println("kern bench — threshold check (cmd/kern/bench_thresholds.json)")
		if len(violations) == 0 {
			fmt.Println("[ok] all thresholds satisfied")
		}
		for _, v := range violations {
			fmt.Printf("[%s] %s: %s\n", v.Level, v.Check, v.Detail)
		}
		if f.json {
			printJSON(map[string]any{"check": "bench-thresholds", "violations": violations})
		}
		for _, v := range violations {
			if v.Level == "fail" {
				os.Exit(1)
			}
		}
	}
	fmt.Printf("wrote %s (%d symbols, %d queries)\n", outPath, res.SymbolCount, len(res.Queries))
}

// benchResult is the schema of .kern/bench.json — the document the web
// console's /benchmarks page renders. Field names are the page's contract;
// change them in lockstep with the golden fixture
// cmd/kern/testdata/bench.json (pinned by
// TestBenchResultRoundTripGoldenFixture — a renamed field here silently
// zeroes the /benchmarks page).
// cliQuery is one fresh-process CLI measurement: the command run in a new
// kern process (what an agent's shell actually pays, including index load)
// and its median/min wall time over repeated runs.
type cliQuery struct {
	Name     string   `json:"name"`
	Args     []string `json:"args"`
	MedianMS float64  `json:"median_ms"`
	MinMS    float64  `json:"min_ms"`
	Runs     int      `json:"runs"`
	OK       bool     `json:"ok"`
	Err      string   `json:"err,omitempty"`
}

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
	CLIQueries     []cliQuery    `json:"cli_queries,omitempty"`
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
// symbol the graph queries resolved; empty for repo-wide scans) and result
// count (the --check zero-result gate reads it).
type queryResult struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Target   string  `json:"target,omitempty"`
	MedianMS float64 `json:"median_ms"`
	MinMS    float64 `json:"min_ms"`
	Runs     int     `json:"runs"`
	Results  int     `json:"results"`
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

// cliSymbol picks a real, defined hub for the CLI measurements: the symbol
// with the most callers in the callers table. topHub's key is a CALLEE name
// (often a stdlib method such as "strings.Contains") which is not a
// definition, so context/explore would exit 1 on it.
func cliSymbol(ix *index.Index) string {
	best, bestN := "main", -1
	for _, s := range ix.Symbols {
		if n := len(ix.Callers[s.FullName()]); n > bestN {
			best, bestN = s.FullName(), n
		}
	}
	return best
}

// heavyTypeSymbol picks the worst query shape for the impact-hub CLI probe:
// the defined TYPE with the most receiver-bearing methods. Types aggregate
// their methods and constructors in the impact surface, so a heavily-used
// type's blast radius dwarfs any single function's — exactly the class of
// query whose latency a leaf-symbol gate cannot see. Falls back to
// cliSymbol's choice when the index has no methods at all.
func heavyTypeSymbol(ix *index.Index) string {
	counts := make(map[string]int)
	for _, s := range ix.Symbols {
		if s.Receiver != "" && s.Kind == "method" {
			counts[s.Receiver]++
		}
	}
	best, bestN := "", -1
	for name, n := range counts {
		if n > bestN {
			best, bestN = name, n
		}
	}
	if best == "" {
		return cliSymbol(ix)
	}
	// Prefer the type's own definition (a struct/type symbol named best) so
	// impact resolves it directly rather than via a method's receiver.
	for _, s := range ix.Symbols {
		if s.Kind == "struct" || s.Kind == "type" || s.Kind == "interface" {
			if s.Name == best {
				return s.FullName()
			}
		}
	}
	return best
}

// measureCLI runs the audit's one-shot CLI commands in fresh processes
// against the persisted store and returns their latency samples. A non-zero
// exit is recorded, never fatal: the bench reports it so --check can fail on
// a broken command rather than a number.
//
// Two validity rules (2026-10-27 audit):
//
//  1. Every command gets one UNTIMED warm-up invocation first. The CLI
//     auto-starts the per-project daemon on first use and pays a one-time
//     lazy-table build inside it; without the warm-up, whether a sample
//     measures the cold spawn or the warm daemon would depend on ambient
//     machine state (a daemon left running by an earlier command), making
//     the gate flaky. The warm-up pins the intended measurement: warm
//     daemon, fresh client process.
//  2. The impact probe runs twice: once on the same leaf-ish symbol as the
//     other commands, and once on the index's heaviest TYPE (impact-hub).
//     A type aggregates its methods and constructors, so its blast radius
//     is the worst query shape the surface serves; gating only a cheap
//     function symbol let real p95s regress invisibly.
func measureCLI(root, symbol, heavyType string) []cliQuery {
	// Under a test binary os.Executable() is the suite itself; spawning it
	// would re-run tests instead of a kern command. CLI rows are for real
	// binaries only.
	if strings.HasSuffix(os.Args[0], ".test") {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	// context/explore/impact resolve a definition name; the hub may be a
	// qualified method ("strings.Contains"), so run them on the bare name.
	simple := symbol
	if i := strings.LastIndexByte(simple, '.'); i >= 0 {
		simple = simple[i+1:]
	}
	heavySimple := heavyType
	if i := strings.LastIndexByte(heavySimple, '.'); i >= 0 {
		heavySimple = heavySimple[i+1:]
	}
	queries := []struct {
		name string
		args []string
	}{
		{"search", []string{"search", symbol, "--limit", "20", "--json"}},
		{"context", []string{"context", simple}},
		{"explore", []string{"explore", simple}},
		{"impact", []string{"impact", simple}},
		{"impact-hub", []string{"impact", heavySimple}},
	}
	out := make([]cliQuery, 0, len(queries))
	for _, q := range queries {
		args := append(append([]string{}, q.args...), "--root", root)
		res := cliQuery{Name: q.name, Args: q.args, Runs: 3, OK: true}
		// Untimed warm-up: absorbs daemon spawn, index hydration and lazy
		// table builds so the timed samples measure the steady state.
		if err := exec.Command(exe, args...).Run(); err != nil {
			// A command that cannot even warm up is a broken command, not
			// a latency: report it the same way a timed failure would.
			res.OK = false
			res.Err = err.Error()
			out = append(out, res)
			continue
		}
		var times []float64
		for i := 0; i < res.Runs; i++ {
			cmd := exec.Command(exe, args...)
			start := time.Now()
			err := cmd.Run()
			times = append(times, float64(time.Since(start).Microseconds())/1000)
			if err != nil {
				res.OK = false
				res.Err = err.Error()
				break
			}
		}
		if len(times) > 0 {
			sort.Float64s(times)
			res.MedianMS = times[len(times)/2]
			res.MinMS = times[0]
		}
		out = append(out, res)
	}
	return out
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

// fmtLatency renders a millisecond sample honestly instead of collapsing fast
// queries to a misleading zero. Three tiers: ms, µs, ns — so a sub-µs query is never shown as 0.
func fmtLatency(msf float64) string {
	if msf >= 1 {
		return fmt.Sprintf("%7.2f ms", msf)
	}
	if msf >= 0.001 {
		return fmt.Sprintf("%7.1f µs", msf*1000)
	}
	return fmt.Sprintf("%7.0f ns", msf*1e6)
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
	fmt.Println("query                kind                    target   median    min     runs  results")
	for _, q := range res.Queries {
		fmt.Printf("%-20s %-22s %-8s %s %s %5d %7d\n", q.Name, q.Kind, q.Target, fmtLatency(q.MedianMS), fmtLatency(q.MinMS), q.Runs, q.Results)
	}
	if len(res.CLIQueries) > 0 {
		fmt.Println()
		fmt.Println("cli (fresh process)  args (truncated)                  median    min    runs  ok")
		for _, q := range res.CLIQueries {
			a := strings.Join(q.Args, " ")
			if len(a) > 32 {
				a = a[:32]
			}
			fmt.Printf("%-20s %-32s %s %s %5d  %v\n", q.Name, a, fmtLatency(q.MedianMS), fmtLatency(q.MinMS), q.Runs, q.OK)
		}
	}
	fmt.Println()
	fmt.Println("methodology: " + res.MethodologyRef)
}

// bench_thresholds.json is the versioned gate config for `kern bench
// --check`: generous multipliers over the measured baseline so the gate
// fails only on REGRESSIONS, never on machine noise. Embedded so the binary
// is self-contained; KERN_BENCH_THRESHOLDS overrides the path for testing.
//
//go:embed bench_thresholds.json
var benchThresholdsRaw []byte

// benchThresholds is the gate configuration: hard ceilings for the warm
// load and every query, a floor for the cold/warm speedup, a warn factor
// (the fraction of a ceiling at which to warn without failing), and the
// minimum result count every query must return (0 disables the
// zero-result check).
type benchThresholds struct {
	WarmLoadMSMax float64            `json:"warm_load_ms_max"`
	QueryMSMax    float64            `json:"query_ms_max"`
	ColdVsWarmMin float64            `json:"cold_vs_warm_min"`
	WarnFactor    float64            `json:"warn_factor"`
	MinResults    int                `json:"min_results"`
	CLIMaxMS      map[string]float64 `json:"cli_ms_max"`
}

// loadBenchThresholds decodes the versioned gate config. A malformed file
// is fatal — the gate must fail loud, not silently skip.
func loadBenchThresholds() benchThresholds {
	raw := benchThresholdsRaw
	if p := os.Getenv("KERN_BENCH_THRESHOLDS"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fatal("Bench: read KERN_BENCH_THRESHOLDS %s: %v", p, err)
		}
		raw = b
	}
	var th benchThresholds
	if err := json.Unmarshal(raw, &th); err != nil {
		fatal("Bench: decode bench thresholds: %v", err)
	}
	return th
}

// benchViolation is one gate finding: a check name, a level ("fail" = exit
// 1, "warn" = report only) and a human detail.
type benchViolation struct {
	Check  string `json:"check"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// checkBenchThresholds verifies a bench run against the gate config. Pure
// function (no I/O) so the gate logic is table-testable. Failures: the
// warm-load ceiling, per-query ceilings, the cold/warm speedup floor
// (guards the persisted-store fast path — a Load that falls through to a
// Build collapses the ratio to ~1x), and zero-result queries (a wiring
// regression, not a latency). Warnings: any metric past warn_factor of its
// ceiling. A ceiling of 0 (or an unmeasured 0 value) disables that check.
func checkBenchThresholds(res benchResult, th benchThresholds) []benchViolation {
	var out []benchViolation
	check := func(name string, val, ceiling float64, unit string) {
		if ceiling <= 0 || val <= 0 {
			return
		}
		if val > ceiling {
			out = append(out, benchViolation{Check: name, Level: "fail",
				Detail: fmt.Sprintf("%.2f%s exceeds the %s ceiling %.2f%s", val, unit, name, ceiling, unit)})
			return
		}
		if th.WarnFactor > 0 && val > ceiling*th.WarnFactor {
			out = append(out, benchViolation{Check: name, Level: "warn",
				Detail: fmt.Sprintf("%.2f%s is past %.0f%% of the %s ceiling %.2f%s", val, unit, th.WarnFactor*100, name, ceiling, unit)})
		}
	}
	check("warm_load_ms", res.WarmLoadMS.MedianMS, th.WarmLoadMSMax, " ms")
	for _, q := range res.Queries {
		check(q.Name+"_ms", q.MedianMS, th.QueryMSMax, " ms")
		if th.MinResults > 0 && q.Results < th.MinResults {
			out = append(out, benchViolation{Check: q.Name + "_results", Level: "fail",
				Detail: fmt.Sprintf("%d results (min %d) — the query wiring regressed, this is not a latency finding", q.Results, th.MinResults)})
		}
	}
	for _, q := range res.CLIQueries {
		if !q.OK {
			out = append(out, benchViolation{Check: "cli_" + q.Name + "_exit", Level: "fail",
				Detail: fmt.Sprintf("fresh-process `kern %s` failed: %s", strings.Join(q.Args, " "), q.Err)})
		}
		check("cli_"+q.Name+"_ms", q.MedianMS, th.CLIMaxMS[q.Name], " ms")
	}
	if th.ColdVsWarmMin > 0 && res.ColdVsWarm < th.ColdVsWarmMin {
		out = append(out, benchViolation{Check: "cold_vs_warm_speedup", Level: "fail",
			Detail: fmt.Sprintf("%.1fx is under the %.1fx floor — the warm load may be falling through to a full build (the persisted-store fast path regressed)", res.ColdVsWarm, th.ColdVsWarmMin)})
	}
	return out
}
