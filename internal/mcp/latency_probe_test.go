package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestMCPLatencyProbe is the env-gated MCP latency audit
// (KERN_MCP_LATENCY=1 — a dated diagnostic, never CI): it drives the REAL
// runTool gate chain (metrics → audit → precheck → validation → handler)
// over the default-surface tools plus representative catalog tools, against
// the kern repo itself (18k+ symbols — realistic worst-case scale), and
// prints a median-sorted table. Every call passes no_cache so the fresh
// handler path is measured, not a D1 cache hit.
func TestMCPLatencyProbe(t *testing.T) {
	if os.Getenv("KERN_MCP_LATENCY") != "1" {
		t.Skip("latency probe: set KERN_MCP_LATENCY=1 (dated diagnostic, never CI)")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Confinement bypass INSIDE the harness (matching the env confinementGate /
	// NewServerForRoot check), so the probe never depends on an external
	// KERN_MCP_NO_CONFINE=1 the exec guard blocks; s.roots is pinned to the
	// repo root below like the benchmark harness.
	t.Setenv("KERN_MCP_NO_CONFINE", "1")
	ctx := context.Background()
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{root}

	// Warm-up: the first index load per root (session cache miss → warm
	// disk load ~hundreds of ms) is reported separately, never amortized
	// into the per-tool numbers.
	warmStart := time.Now()
	if _, err := s.runTool(ctx, "warmup", "", "kern_search", map[string]any{
		"query": "runExec", "root": root,
	}); err != nil {
		t.Fatalf("warm-up call failed: %v", err)
	}
	fmt.Printf("index warm-up (first kern_search, includes warm load): %v\n", time.Since(warmStart).Round(time.Millisecond))

	probes := []struct {
		name string
		args map[string]any
		reps int
	}{
		{"kern_search", map[string]any{"query": "runExec", "root": root, "no_cache": "1"}, 5},
		{"kern_explore", map[string]any{"symbol": "runExec", "root": root, "no_cache": "1"}, 5},
		{"kern_impact", map[string]any{"change": "runExec", "root": root, "no_cache": "1"}, 5},
		{"kern_meta", map[string]any{"request": "find the NewServer function", "root": root, "no_cache": "1"}, 5},
		{"kern_buddy", map[string]any{"root": root, "no_cache": "1"}, 3},
		{"kern_verify", map[string]any{"root": root, "no_cache": "1"}, 5},
		{"kern_context", map[string]any{"symbol": "runExec", "root": root, "no_cache": "1"}, 5},
		{"kern_search", map[string]any{"root": root, "query": "runExec", "no_cache": "1"}, 5},
		{"kern_exec", map[string]any{"code": "echo ok", "lang": "bash", "root": root}, 5},
		{"kern_verify", map[string]any{"fast": "true", "root": root, "no_cache": "1"}, 1},
	}

	// kern_verify is excluded unless KERN_MCP_LATENCY_VERIFY=1: its changed-
	// package build+test tier takes minutes (real go build/test work, not
	// server overhead) and drowns the table — it was the baseline's top row
	// (10m16s for one changed package); measure it deliberately, not on
	// every probe run.
	includeVerify := os.Getenv("KERN_MCP_LATENCY_VERIFY") == "1"

	type row struct {
		name         string
		median, minM time.Duration
		maxM         time.Duration
		first        string
		err          string
	}
	var rows []row
	for _, p := range probes {
		if !includeVerify && p.name == "kern_verify" {
			continue
		}
		var durs []time.Duration
		var first, errStr string
		for i := 0; i < p.reps; i++ {
			start := time.Now()
			out, err := s.runTool(ctx, fmt.Sprintf("probe-%s-%d", p.name, i), "", p.name, p.args)
			durs = append(durs, time.Since(start))
			if err != nil {
				errStr = err.Error()
				break
			}
			if i == 0 {
				first = out
			}
		}
		if len(durs) == 0 {
			continue
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		med := durs[len(durs)/2]
		r := row{name: p.name, median: med, minM: durs[0], maxM: durs[len(durs)-1], first: first, err: errStr}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].median > rows[j].median })

	fmt.Println("\n=== MCP tool latency (median over reps, no_cache, warm index) ===")
	for _, r := range rows {
		status := ""
		if r.err != "" {
			status = "ERROR: " + r.err
		} else if len(r.first) > 60 {
			status = strings.ReplaceAll(r.first[:60], "\n", " ") + "…"
		} else {
			status = strings.ReplaceAll(r.first, "\n", " ")
		}
		fmt.Printf("%-16s median %8v  (min %8v, max %8v)  %s\n", r.name, r.median.Round(time.Millisecond), r.minM.Round(time.Millisecond), r.maxM.Round(time.Millisecond), status)
	}
}
