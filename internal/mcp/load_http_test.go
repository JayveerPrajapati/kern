package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// loadObservation is a single measured tools/call result from a load run.
type loadObservation struct {
	Duration   time.Duration
	Err        error
	TextBytes  int
	Provenance bool
}

// loadResult aggregates one tool's load profile.
type loadResult struct {
	Tool         string
	Profile      string
	Persona      e2ePersona
	Requests     int
	Concurrency  int
	Successes    int
	ToolErrors   int
	TransportErr int
	P50          time.Duration
	P95          time.Duration
	P99          time.Duration
	Max          time.Duration
	Throughput   float64 `json:"throughput"`
	Provenance   int     `json:"provenance"`

	// Operational evidence required by the load plan. The in-process MCP
	// harness has no daemon hop, while cache deltas and RSS make the report
	// useful for finding regressions rather than only latency outliers.
	RSSStart    uint64 `json:"rss_start_bytes"`
	RSSEnd      uint64 `json:"rss_end_bytes"`
	RSSDelta    int64  `json:"rss_delta_bytes"`
	CacheHits   int64  `json:"cache_hits"`
	CacheMisses int64  `json:"cache_misses"`
	ServedBy    string `json:"served_by"`
}

// TestLoad_kern_search runs a small, deterministic concurrency slice against a
// generated repository. It is the first load fixture in the full-surface
// plan: one tool, one persona, one command at a time.
func TestLoad_kern_search(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          20,
		SymbolsPerFile: 5,
		DeepCallGraph:  true,
	})
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")

	s := newHTTPServer()
	before := goroutineCount()
	result := runToolLoad(t, s, e2eLoadCase{
		Name:        "smoke",
		Tool:        "kern_search",
		Persona:     personaDeveloper,
		Requests:    20,
		Concurrency: 4,
		Timeout:     5 * time.Second,
	}, func(i int) map[string]any {
		return map[string]any{
			"root":  root,
			"query": fmt.Sprintf("Symbol%04d", i),
			"limit": 5,
		}
	})

	if result.ToolErrors != 0 {
		t.Errorf("kern_search load produced tool errors: %d", result.ToolErrors)
	}
	if result.TransportErr != 0 {
		t.Errorf("kern_search load produced transport errors: %d", result.TransportErr)
	}
	if result.Successes != 20 {
		t.Errorf("kern_search load successes = %d, want 20", result.Successes)
	}
	if result.P99 <= 0 {
		t.Errorf("kern_search load P99 was not positive: %s", result.P99)
	}
	assertNoGoroutineLeak(t, before)
}

// runToolLoad executes a single tool under one load profile and returns the
// aggregated latency and correctness evidence.
func runToolLoad(t *testing.T, s *Server, profile e2eLoadCase, makeArgs func(i int) map[string]any) loadResult {
	t.Helper()

	if profile.Requests < 1 {
		profile.Requests = 1
	}
	if profile.Concurrency < 1 {
		profile.Concurrency = 1
	}

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	metricsBefore := metrics.Default().Snapshot()

	observations := make([]loadObservation, 0, profile.Requests)
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)

	for worker := 0; worker < profile.Concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range jobs {
				obs := e2eCallLoad(t, s, i+1, profile.Tool, makeArgs(i))
				mu.Lock()
				observations = append(observations, obs)
				mu.Unlock()
			}
		}(worker)
	}

	start := time.Now()
	for i := 0; i < profile.Requests; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&memAfter)
	metricsAfter := metrics.Default().Snapshot()

	result := summarizeLoad(t, profile, observations, elapsed)
	result.RSSStart = memBefore.Sys
	result.RSSEnd = memAfter.Sys
	result.RSSDelta = int64(memAfter.Sys) - int64(memBefore.Sys)
	result.CacheHits = metricsAfter.CacheHits - metricsBefore.CacheHits
	result.CacheMisses = metricsAfter.CacheMisses - metricsBefore.CacheMisses
	result.ServedBy = "in_process"
	recordLoadResult(result)
	return result
}

// e2eCallLoad performs one JSON-RPC tools/call without failing the test on a
// single bad response; the aggregate result records correctness separately.
func e2eCallLoad(t *testing.T, s *Server, id int, tool string, args map[string]any) loadObservation {
	start := time.Now()

	raw, err := json.Marshal(args)
	if err != nil {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("marshal args: %w", err)}
	}
	body := `{"jsonrpc":"2.0","id":` + jsonID(id) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + string(raw) + `}}`
	rr := doHTTP(t, s, http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("HTTP %d: %s", rr.Code, rr.Body.String())}
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("unmarshal response: %w", err)}
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("rpc error: %v", rpcErr)}
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("missing result object: %#v", resp)}
	}

	text := ""
	if content, ok := result["content"].([]any); ok && len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			text, _ = first["text"].(string)
		}
	}
	_, hasProvenance := result["provenance"].(map[string]any)
	isErr, _ := result["isError"].(bool)
	if isErr {
		return loadObservation{Duration: time.Since(start), Err: fmt.Errorf("tool error: %s", text), TextBytes: len(text), Provenance: hasProvenance}
	}

	return loadObservation{Duration: time.Since(start), TextBytes: len(text), Provenance: hasProvenance}
}

func summarizeLoad(t *testing.T, profile e2eLoadCase, observations []loadObservation, elapsed time.Duration) loadResult {
	t.Helper()

	durations := make([]time.Duration, 0, len(observations))
	result := loadResult{
		Tool:        profile.Tool,
		Profile:     profile.Name,
		Persona:     profile.Persona,
		Requests:    len(observations),
		Concurrency: profile.Concurrency,
	}

	for _, obs := range observations {
		durations = append(durations, obs.Duration)
		if obs.Err != nil {
			if obs.TextBytes > 0 {
				result.ToolErrors++
			} else {
				result.TransportErr++
			}
			continue
		}
		result.Successes++
		if obs.Provenance {
			result.Provenance++
		}
	}

	if len(durations) == 0 {
		return result
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	result.P50 = percentile(durations, 0.50)
	result.P95 = percentile(durations, 0.95)
	result.P99 = percentile(durations, 0.99)
	result.Max = durations[len(durations)-1]
	if elapsed > 0 {
		result.Throughput = float64(len(observations)) / elapsed.Seconds()
	}
	return result
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func goroutineCount() int {
	return runtime.NumGoroutine()
}

func TestLoad_kern_explore(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          10,
		SymbolsPerFile: 5,
		DeepCallGraph:  true,
	})
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")

	s := newHTTPServer()
	before := goroutineCount()
	result := runToolLoad(t, s, e2eLoadCase{
		Name:        "smoke",
		Tool:        "kern_explore",
		Persona:     personaRefactorer,
		Requests:    10,
		Concurrency: 2,
		Timeout:     5 * time.Second,
	}, func(i int) map[string]any {
		return map[string]any{"root": root, "symbol": "Symbol0000", "mode": "balanced"}
	})
	assertE2ELoadOK(t, result, 10)
	assertNoGoroutineLeak(t, before)
}

func TestLoad_kern_graph(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          10,
		SymbolsPerFile: 5,
		DeepCallGraph:  true,
	})
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")

	s := newHTTPServer()
	before := goroutineCount()
	result := runToolLoad(t, s, e2eLoadCase{
		Name:        "smoke",
		Tool:        "kern_graph",
		Persona:     personaArchitect,
		Requests:    10,
		Concurrency: 2,
		Timeout:     5 * time.Second,
	}, func(i int) map[string]any {
		return map[string]any{"root": root, "symbol": "Symbol0000", "format": "one-line"}
	})
	assertE2ELoadOK(t, result, 10)
	assertNoGoroutineLeak(t, before)
}
