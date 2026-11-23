package main

// T-07 benchmark gate: the sections that measure the fast-read campaign's
// behavioral targets — guard classifier latency (fresh vs the guardrulecache
// fast path), kern_verify run reuse, and instruction (policy) byte cost.
// Everything feeds benchResult (`.kern/bench.json`) and the versioned
// threshold gate in cmd/kern/bench_thresholds.json, so `kern bench --check`
// fails on regressions of each behavior the campaign shipped. Latency rows
// report median AND p95 — never a single wall-clock.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/guardrule"
	"github.com/JayveerPrajapati/kern/internal/guardrulecache"
	"github.com/JayveerPrajapati/kern/internal/verifycmd"
)

// t07Sample is the campaign's comparison contract: median and p95 across N
// runs, never wall-clock-only.
type t07Sample struct {
	MedianMS float64 `json:"median_ms"`
	P95MS    float64 `json:"p95_ms"`
	Runs     int     `json:"runs"`
}

func t07FromDurations(ds []time.Duration) t07Sample {
	if len(ds) == 0 {
		return t07Sample{}
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	p95 := func(q float64) int {
		i := int(q*float64(len(s)-1) + 0.5)
		if i < 0 {
			i = 0
		}
		if i >= len(s) {
			i = len(s) - 1
		}
		return i
	}
	return t07Sample{MedianMS: ms(s[len(s)/2]), P95MS: ms(s[p95(0.95)]), Runs: len(s)}
}

// ---------------------------- guard latency ----------------------------

type guardRow struct {
	Name    string    `json:"name"`
	Command string    `json:"command"`
	Verdict string    `json:"verdict"`
	Fresh   t07Sample `json:"fresh"`
	Cached  t07Sample `json:"cached"`
}

type guardSection struct {
	Rows []guardRow `json:"rows"`
	Note string     `json:"note"`
}

// measureGuardSection times the command classifier both ways for one command
// of each verdict class: fresh (guardrule.ClassifyCommandInRoots, what a
// cache-less build pays) and cached (guardrulecache, the T-02 fast path).
func measureGuardSection(root string) *guardSection {
	rows := []struct{ name, cmd string }{
		{"read", "cat README.md"},
		{"grep", "grep -rn TODO ."},
		{"gate", "rm -rf internal/guard"},
		{"governed", "go build ./..."},
	}
	sec := &guardSection{Note: "fresh = guardrule.ClassifyCommandInRoots; cached = guardrulecache (T-02). GATE is never cached by design — its cached arm measures the miss path, which re-classifies fresh and denies."}
	for _, r := range rows {
		fresh := make([]time.Duration, 0, 9)
		for i := 0; i < 9; i++ {
			start := time.Now()
			guardrule.ClassifyCommandInRoots(r.cmd, root, nil)
			fresh = append(fresh, time.Since(start))
		}
		res := guardrulecache.ClassifyCommandInRoots(r.cmd, root, nil) // prime
		cached := make([]time.Duration, 0, 9)
		for i := 0; i < 9; i++ {
			start := time.Now()
			guardrulecache.ClassifyCommandInRoots(r.cmd, root, nil)
			cached = append(cached, time.Since(start))
		}
		sec.Rows = append(sec.Rows, guardRow{
			Name: r.name, Command: r.cmd, Verdict: string(res.Verdict),
			Fresh: t07FromDurations(fresh), Cached: t07FromDurations(cached),
		})
	}
	return sec
}

// ---------------------------- verify reuse ----------------------------

type verifyReuseSample struct {
	Command  string    `json:"command"`
	FreshMS  t07Sample `json:"fresh_ms"`
	ReusedMS t07Sample `json:"reused_ms"`
	Speedup  float64   `json:"speedup"` // fresh median / reused median
	Note     string    `json:"note,omitempty"`
}

// measureVerifyReuse times kern_verify's command mode fresh (manifest wiped
// before every run — the pre-T-02 cost) vs reused (the anchor-reuse fast
// path). The payload command is a real, allowed, side-effect-free git read so
// the measurement includes the command the way an agent actually runs it.
func measureVerifyReuse(root string) *verifyReuseSample {
	const cmd = "git log --oneline -3"
	run := func() (time.Duration, error) {
		start := time.Now()
		_, _, err := verifycmd.VerifyCommand(context.Background(), map[string]any{
			"command": cmd, "root": root,
		})
		return time.Since(start), err
	}
	res := &verifyReuseSample{Command: cmd}

	var freshErr error
	fresh := make([]time.Duration, 0, 5)
	for i := 0; i < 5; i++ {
		// RunsDir is verifycmd's own locator, so this cannot drift from the
		// real storage path. Removing it forces save-not-lookup every run.
		if err := os.RemoveAll(verifycmd.RunsDir(root)); err != nil {
			freshErr = err
			break
		}
		d, err := run()
		if err != nil {
			freshErr = err
			break
		}
		fresh = append(fresh, d)
	}
	if freshErr != nil {
		res.Note = fmt.Sprintf("fresh arm failed: %v", freshErr)
		return res
	}
	reused := make([]time.Duration, 0, 5)
	for i := 0; i < 5; i++ {
		d, err := run()
		if err != nil {
			res.Note = fmt.Sprintf("reused arm failed: %v", err)
			return res
		}
		reused = append(reused, d)
	}
	res.FreshMS = t07FromDurations(fresh)
	res.ReusedMS = t07FromDurations(reused)
	if res.ReusedMS.MedianMS > 0 {
		res.Speedup = res.FreshMS.MedianMS / res.ReusedMS.MedianMS
	}
	return res
}

// --------------------------- instruction cost ---------------------------

type instructionCost struct {
	GlobalPolicyBytes int    `json:"global_policy_bytes"`
	RepoPolicyBytes   int    `json:"repo_policy_bytes"`
	TokenEstimate     int    `json:"token_estimate"`
	Note              string `json:"note"`
}

// measureInstructions reports the default per-session instruction payload:
// the condensed global rules block (when `kern setup --global-rules` wired
// it) plus the thin repo policy (the T-05 <=2KB core block, the widest
// default wiring). Token estimate is the standard bytes/4 heuristic.
func measureInstructions() *instructionCost {
	res := &instructionCost{Note: "global = agentwire.CondenseGlobalRules (wired host slot); repo = agentwire.ThinAGENTSmd default core block (T-05); tokens = bytes/4"}
	gb, err := agentwire.CondenseGlobalRules()
	if err != nil {
		res.Note = fmt.Sprintf("global rules unavailable (%v); repo policy only", err)
	}
	res.GlobalPolicyBytes = len(gb)
	res.RepoPolicyBytes = len(agentwire.ThinAGENTSmd("all"))
	res.TokenEstimate = (res.GlobalPolicyBytes + res.RepoPolicyBytes) / 4
	return res
}
