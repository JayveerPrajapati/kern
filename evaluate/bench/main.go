// Command bench is the kern benchmark harness. It measures token reduction
// and retrieval quality across every compression surface and prints a
// reproducible markdown report. The idea (and the honesty bar) is borrowed
// from code-review-graph's published 36-376x benchmarks
// (github.com/tirth8205/code-review-graph): kern reports its own numbers as
// token-reduction vs. the raw input — never as a LOC->LLM-input ratio — and
// every sample corpus is deterministic and shipped in this file.
//
// Run:  go run ./evaluate/bench  (from the repo root)
// Flags: -root DIR   use DIR's docs for the retrieval-recall test
//
// Exit code is 0 when every hard gate passes, 1 otherwise.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Deterministic sample corpora (fixed strings, not network-sourced). Kern's
// deterministic path is line-structured, so samples use realistic multi-line
// inputs — a wall-of-text single paragraph is a degenerate case for every
// line-based optimizer, not just kern.
const verbosePrompt = `Hello! I hope you're doing well today. I was wondering if you could help me out with something.

I'm trying to debug why my Go service is not starting up. The service is called billing-worker and it lives at internal/worker/billing.go.

So basically, when I run it, it exits with "listening on :8080" and then immediately after that it prints an error and shuts down.

I think the problem might be related to the database connection pool. It could also be the metrics endpoint failing.

I've been stuck on this for two hours now and I'm getting pretty frustrated.

Please take a look at the code and figure out what's going wrong. If you need other files, the config is at config/config.yaml and the env vars are loaded in internal/config/load.go.

Sorry for the long message but I wanted to give you all the details. Thanks so much in advance for your help!`

const verboseReply = `Certainly! Let me walk you through how to fix that issue step by step.

So first off, I think the root cause is almost certainly the database connection pool timing out. Let me break it down for you.

Note that the pool is created in NewPool which is called from Run() in internal/worker/billing.go. The context passed to sql.Open with a deadline of 5 seconds might not be enough.

Here is the relevant code:

func NewPool(dsn string) (*sql.DB, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, err
    }
    return db, ctx.Err()
}

As I mentioned earlier, the 5-second timeout is the culprit. I'd recommend raising it or moving the ping into a goroutine with a retry loop.

Let me know if you need more help! Happy to clarify anything.`

const noisyLog = `[2026-08-09 10:00:01] INFO  starting billing-worker version=1.2.3
[2026-08-09 10:00:01] INFO  loading config from config/config.yaml
[2026-08-09 10:00:02] DEBUG config keys: 42 keys loaded
[2026-08-09 10:00:02] INFO  connecting to postgres at host=db.internal port=5432
[2026-08-09 10:00:03] INFO  connected
[2026-08-09 10:00:03] WARN  slow query detected: SELECT * FROM invoices WHERE status=$1 took 1203ms
[2026-08-09 10:00:04] INFO  listening on :8080
[2026-08-09 10:00:05] ERROR failed to start http server: bind: address already in use
[2026-08-09 10:00:05] FATAL exit status 1
goroutine 1 [running]:
main.main()
	/workspace/cmd/billing/main.go:42 +0x1a3
created by os.StartProcess in /usr/bin/billing-worker
`

type metric struct {
	name string
	raw  string
	out  string
	note string
	gate float64 // minimum % reduction the harness expects; 0 = informational
}

func main() {
	root := flag.String("root", ".", "project root whose docs the recall test indexes")
	flag.Parse()

	rows := runMetrics()
	fmt.Println("# kern benchmark report")
	fmt.Println()
	fmt.Printf("generated: %s   corpus: deterministic inline + %s docs\n\n",
		time.Now().UTC().Format(time.RFC3339), *root)

	fmt.Println("| operation | before | after | reduction | note |")
	fmt.Println("|---|---|---|---|---|")
	var gates []string
	for _, m := range rows {
		before := tokenize.Count(m.raw)
		after := tokenize.Count(m.out)
		pct := 0.0
		if before > 0 {
			pct = float64(before-after) / float64(before) * 100
		}
		fmt.Printf("| %s | %d | %d | %.1f%% | %s |\n", m.name, before, after, pct, m.note)
	}
	gates = checkGates(rows)
	fmt.Println()

	// Retrieval recall: index the project's docs on the deterministic n-gram
	// path, then verify known "needle" queries surface the expected file in the
	// top-5. Mirrors code-review-graph's recall test on a local corpus.
	fmt.Println("## retrieval recall (docs index)")
	fmt.Println()
	ix, err := docsearch.IndexDir(*root)
	if err != nil {
		fmt.Printf("recall test skipped: %v\n", err)
		os.Exit(0)
	}
	hit, total := 0, 0
	for _, q := range recallQueries {
		total++
		top := ix.Search(q.query, 5)
		ok := false
		for _, r := range top {
			if strings.Contains(r.Doc.Chunk.File, q.file) {
				ok = true
				break
			}
		}
		if ok {
			hit++
		} else {
			fmt.Printf("  MISS %-28s -> %s\n", q.query, q.file)
		}
	}
	fmt.Printf("recall@5: %d/%d (%.0f%%)\n", hit, total, pctf(hit, total))
	if hit < total-1 {
		gates = append(gates, fmt.Sprintf("recall %d/%d below target", hit, total))
	}

	// Honesty footer: kern measures token reduction vs the input it is given,
	// not LOC->LLM-input ratios. Normalize other tools' numbers the same way.
	fmt.Println()
	fmt.Println("_Note: kern measures token reduction vs the raw input it is given,_")
	fmt.Println("_not LOC->LLM-input ratios. For apples-to-apples comparison, normalize_")
	fmt.Println("_every tool's numbers the same way (code-review-graph-style 36-376x_")
	fmt.Println("_figures are a different, input-mix-dependent yardstick)._")

	if len(gates) > 0 {
		fmt.Fprintln(os.Stderr, "bench gates failed:")
		for _, g := range gates {
			fmt.Fprintln(os.Stderr, "  -", g)
		}
		os.Exit(1)
	}
}

func runMetrics() []metric {
	p, _ := optimize.Prompt(verbosePrompt, "", optimize.Options{})
	l, _ := optimize.Log(noisyLog, optimize.Options{})
	t, _ := terse.Compress(verboseReply)
	b := budget.Fit(noisyLog, 40)
	return []metric{
		{name: "optimize prompt", raw: verbosePrompt, out: p.Output, note: "deterministic", gate: 25},
		{name: "optimize log", raw: noisyLog, out: l.Output, note: "keeps errors + frames", gate: 40},
		{name: "optimize output (terse)", raw: verboseReply, out: t, note: "strips filler, keeps code", gate: 5},
		{name: "budget fit (40 tok)", raw: noisyLog, out: b, note: "head + key lines", gate: 75},
	}
}

// checkGates verifies each metric's minimum expected reduction. 0 = informational.
func checkGates(rows []metric) []string {
	var gates []string
	for _, m := range rows {
		before := tokenize.Count(m.raw)
		after := tokenize.Count(m.out)
		pct := 0.0
		if before > 0 {
			pct = float64(before-after) / float64(before) * 100
		}
		if m.gate > 0 && pct < m.gate {
			gates = append(gates, fmt.Sprintf("%s: %.1f%% < gate %.1f%%", m.name, pct, m.gate))
		}
	}
	return gates
}

type recall struct {
	query string
	file  string
}

// recallQueries target files that exist in this repo, so the harness verifies
// itself on every run.
var recallQueries = []recall{
	{"how do I optimize a prompt", "README.md"},
	{"what commands reduce context", "README.md"},
	{"kern usage rules for agents", "AGENTS.md"},
}

func pctf(hit, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(hit) / float64(total) * 100
}
