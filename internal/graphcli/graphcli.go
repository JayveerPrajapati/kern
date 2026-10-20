// Package graphcli implements the graph/index/search command family of the
// kern CLI: `kern graph`, `kern index`, `kern search`, `kern repos`, `kern
// ast`, `kern fts`, `kern precache`, `kern watch` and the graph-report
// subcommands (explore, why, twin, path, near, ...). It was extracted from
// cmd/kern (Phase 3 of the CLI shrink) following the internal/approvalcli
// and internal/verifycmd precedents: the package owns the family's flag
// parsing, rendering and internal helpers, and exports Run* entrypoints
// that return the process exit code (int) to the cmd/kern dispatch table,
// which wires them via the commandTable like the approvalcli runners.
//
// Exit-code contract (pinned by cmd/kern tests, must survive EXACTLY):
//
//	usage errors  -> 2 (no stack trace)
//	findings / runtime errors -> 1
//
// The command bodies keep the house fatal style (panic with the exitError
// sentinel); each Run* entrypoint recovers the sentinel and converts it back
// into the exit code, so cmd/kern's main() never sees a foreign panic and
// the dispatch table always receives a plain int.
package graphcli

import (
	"fmt"
	"os"

	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version reported in the search
// --json payloads, mirroring the cmd/kern main package version var the
// moved code read. Same "dev" initializer + kversion.Adopt init() contract
// so both ldflags stamp forms keep working (the modern
// -X .../internal/version.Version=... form reaches this copy via Adopt; the
// legacy -X main.version=... form keeps working in cmd/kern itself).
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

// exitError is the sentinel panic type used by fatal and fatalUsage — the
// cmd/kern helpers.go convention, preserved verbatim. It is recovered inside
// this package by run (each exported entrypoint), which converts it back
// into the process exit code.
type exitError struct{ code int }

// fatal prints an error to stderr and signals exit code 1 (the Unix
// convention for runtime errors).
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and signals exit code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown subcommands. It must never print a stack trace.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// run executes a family command body, recovering the exitError sentinel and
// returning its code — the shared wrapper behind every exported entrypoint.
// Non-sentinel panics are re-panicked (a genuine bug must not be swallowed
// into an exit code).
func run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r) // re-panic non-sentinel
		}
	}()
	return fn()
}

// RunGraph executes `kern graph <symbol> [root] [flags]` — call-graph
// context (including the `graph dead` subcommand alias for `kern dead`).
func RunGraph(rest []string) int {
	return run(func() int { runGraph(rest); return 0 })
}

// RunInherits executes `kern inherits <symbol> [root] [--json]` — class
// hierarchy.
func RunInherits(rest []string) int {
	return run(func() int { runInherits(rest); return 0 })
}

// RunWhy executes `kern why <symbol> [root] [--json]` — rationale/doc
// report.
func RunWhy(rest []string) int {
	return run(func() int { runWhy(rest); return 0 })
}

// RunWiki executes `kern wiki [flags]` — repo digest.
func RunWiki(rest []string) int {
	return run(func() int { runWiki(rest); return 0 })
}

// RunHubs executes `kern hubs [flags]` — hotspots.
func RunHubs(rest []string) int {
	return run(func() int { runHubs(rest); return 0 })
}

// RunBridges executes `kern bridges [flags]` — coupling points.
func RunBridges(rest []string) int {
	return run(func() int { runBridges(rest); return 0 })
}

// RunTestgaps executes `kern testgaps [flags]` — test coverage gap
// analysis (also the `test-gaps` alias).
func RunTestgaps(rest []string) int {
	return run(func() int { runTestgaps(rest); return 0 })
}

// RunFlows executes `kern flows [flags]` — call flows.
func RunFlows(rest []string) int {
	return run(func() int { runFlows(rest); return 0 })
}

// RunCommunities executes `kern communities [flags]` — subsystem clusters.
func RunCommunities(rest []string) int {
	return run(func() int { runCommunities(rest); return 0 })
}

// RunPath executes `kern path <from-symbol> <to-symbol> [root]` (or
// --from/--to) — shortest call path.
func RunPath(rest []string) int {
	return run(func() int { runPath(rest); return 0 })
}

// RunDead executes `kern dead [flags]` — dead-code detection.
func RunDead(rest []string) int {
	return run(func() int { runDead(rest); return 0 })
}

// RunSurprising executes `kern surprising [flags]` — cross-community call
// edges ranked by community distance x rarity.
func RunSurprising(rest []string) int {
	return run(func() int { runSurprising(rest); return 0 })
}

// RunCycles executes `kern cycles [flags]` — package-level import cycles
// (Tarjan SCC).
func RunCycles(rest []string) int {
	return run(func() int { runCycles(rest); return 0 })
}

// RunLarges executes `kern larges [flags]` — god functions.
func RunLarges(rest []string) int {
	return run(func() int { runLarges(rest); return 0 })
}

// RunArch executes `kern arch [flags]` — architecture overview.
func RunArch(rest []string) int {
	return run(func() int { runArch(rest); return 0 })
}

// RunChurn executes `kern churn [flags]` — change-frequency risk.
func RunChurn(rest []string) int {
	return run(func() int { runChurn(rest); return 0 })
}

// RunCochange executes `kern cochange [flags]` — co-change coupling.
func RunCochange(rest []string) int {
	return run(func() int { runCochange(rest); return 0 })
}

// RunExplore executes `kern explore <symbol> [root] [flags]` — symbol
// source + blast radius.
func RunExplore(rest []string) int {
	return run(func() int { runExplore(rest); return 0 })
}

// RunNear executes `kern near <symbol> [root] [--depth N] [--max N]` —
// dependency-tree walk (also the `walk` alias).
func RunNear(rest []string) int {
	return run(func() int { runNear(rest); return 0 })
}

// RunProbe executes `kern probe "<task text>" [root] [--max N]` —
// task-driven context bundle.
func RunProbe(rest []string) int {
	return run(func() int { runProbe(rest); return 0 })
}

// RunTrace executes `kern trace <file|- for stdin> [root] [--limit N]` —
// runtime-impact overlay.
func RunTrace(rest []string) int {
	return run(func() int { runTrace(rest); return 0 })
}

// RunTwin executes `kern twin [flags]` — software twin.
func RunTwin(rest []string) int {
	return run(func() int { runTwin(rest); return 0 })
}

// RunIndex executes `kern index [root] [flags]` — (re)build the symbol
// index; `--status` reports health; `ensure-fresh` probes+rebuilds+re-verifies
// in one subprocess; `where` reports the resolved store path.
func RunIndex(rest []string) int {
	return run(func() int { runIndex(rest); return 0 })
}

// RunWatch executes `kern watch [flags]` — watch & reindex.
func RunWatch(rest []string) int {
	return run(func() int { runWatch(rest); return 0 })
}

// RunAst executes `kern ast <pattern> [root] [--all]` — AST symbol search.
func RunAst(rest []string) int {
	return run(func() int { runAst(rest); return 0 })
}

// RunRepos executes `kern repos (list|add <path> [name]|search
// <query>|remove <name>)` — multi-repo search.
func RunRepos(rest []string) int {
	return run(func() int { runRepos(rest); return 0 })
}

// RunSearch executes `kern search <query> [root] [--limit N] [--repos]
// [--json] [--semantic]` — ranked symbol search.
func RunSearch(rest []string) int {
	return run(func() int { runSearch(rest); return 0 })
}

// RunFts executes `kern fts "<query>" [root] [--limit N]` — FTS5 search.
func RunFts(rest []string) int {
	return run(func() int { runFts(rest); return 0 })
}

// RunPrecache executes `kern precache [flags]` — warm caches (single pass
// by default; `--watch` opts into the daemon).
func RunPrecache(rest []string) int {
	return run(func() int { runPrecache(rest); return 0 })
}
