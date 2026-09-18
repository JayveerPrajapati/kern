// Command kern is the local context optimizer: prompt compression, log
// stripping, project mapping, compact build runs and token savings reports.

package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version, initialized from the shared
// internal/version.Version so every kern binary reports the same value.
// It starts as the literal "dev" (not a copy of kversion.Version) because
// the legacy -ldflags "-X main.version=..." only rewrites a variable whose
// initializer is a compile-time constant: a runtime copy from another global
// aliases the read and silently defeats -X. When unstamped, init() adopts
// the shared internal/version.Version (default "dev", or the newer
// "-X github.com/JayveerPrajapati/kern/internal/version.Version=..." form).
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

func usage() {
	fmt.Fprintf(os.Stderr, `kern - local context, blast-radius & governance engine for AI agents & developers.

Core Workflows (The 5 Essential Verbs):
  kern meta "<request>"                           Natural language intent router (dispatches to any tool)
  kern explore <symbol|file> [--arch] [--json]    Deep symbol/file exploration (source, call flow, blast radius)
  kern search <query> [--repos] [--semantic]      AST symbol & full-text search across codebase
  kern plan <symbol|task> [--json]                Blast-radius impact & surgical refactor planning
  kern mutate | kern rename | kern refactor       Safe AST transformations, renames & test gap sensitivity
  kern verify [types] [--types build,test,sec]    Unified build/test/firewall gate validation (G0-G39; G10 retired)

Context & Token Optimization:
  kern optimize <prompt> [--fewshot] [--mask]     Compress & fine-tune prompts with project memory
  kern fit-context <symbol|file> [--budget N]     Multi-tier adaptive token window compressor
  kern pack [root] [--max-tokens N] [--graph]     Token-dense context bundle for LLMs
  kern compact <file>                             Symbolic signature summary of a file

Agent & Session Setup:
  kern onboard [root]                             Session start: register, index & wire agents
  kern buddy [root]                               Session digest & conventions for incoming agents
  kern setup [--detect] [--global]                Wire kern-first tools into AI agents (MCP/OpenCode/Claude)
  kern doctor [root] [--json]                     Diagnostic report (binary, index, wiring, freshness)

Tip: Run 'kern --all' or 'kern help --all' to list all 140+ specialized micro-commands.
`)
}

func usageAll() {
	// Generated from commandTable so the catalog can never drift from the
	// registered command set (kern --all previously listed only ~100 of
	// 203 registered commands). Help text one-liner when present, otherwise
	// the usage first line.
	fmt.Fprintf(os.Stderr, "kern - kern your context. Complete CLI catalog.\n\nUsage:\n")
	names := slices.Sorted(maps.Keys(commandTable))
	for _, name := range names {
		e := commandTable[name]
		desc := e.help
		line := "  kern " + name
		if desc == "" && e.usage != "" {
			u := strings.TrimPrefix(e.usage, "usage: kern ")
			u = strings.TrimPrefix(u, name)
			u = strings.TrimSpace(u)
			u = strings.TrimPrefix(u, "[flags]")
			u = strings.TrimSpace(u)
			if i := strings.IndexByte(u, '\n'); i >= 0 {
				u = u[:i]
			}
			desc = u
		}
		if desc != "" {
			line += "  " + desc
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

func main() {
	cmd, rest := resolveCommandAndFlags()
	// Inject the live tool catalog into the diff-gate drift checks at startup — explicitly, never via init().
	// This covers pure CLI paths (`kern diff-gate`, `kern gen-catalog`) that
	// never construct an MCP server; server binaries are also wired inside
	// mcp.NewServer. A binary that skips both leaves the drift checks without
	// a catalog and they fail loud (StatusError), never silent SKIP.
	catalog.WithDiffgateTools()

	// Load prior metrics snapshot from disk so CLI metrics accumulate across
	// invocations (F-46/F-47/F-56). The `stats performance --reset` command
	// clears the persisted file before rendering; all other commands load the
	// prior state on startup and save the updated state on exit.
	metricsPath := cache.Path("metrics.json")
	isStatsPerfReset := cmd == "stats" && len(rest) > 0 && rest[0] == "performance" && hasFlag(rest[1:], "--reset")
	if !isStatsPerfReset {
		_ = metrics.Default().Load(metricsPath) // best-effort; missing file is fine
	}

	// dispatchCommand is wrapped in a func literal that recovers the
	// exitError sentinel panicked by fatal/fatalUsage (and other handler
	// exits routed through it) and converts it back into the exit code. This
	// keeps every exit on the single path below so the metrics snapshot is
	// persisted before os.Exit runs.
	code := func() (c int) {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(exitError); ok {
					c = e.code
					return
				}
				panic(r) // re-panic non-sentinel
			}
		}()
		return dispatchCommand(cmd, rest)
	}()

	// Persist the updated snapshot before exiting. Best-effort: a write
	// failure is non-fatal (metrics are non-critical). This runs explicitly
	// (not via defer) because os.Exit below does not run deferred functions.
	// For `stats performance --reset`, runStatsPerformance handles persistence
	// (Reset + Save) in-place.
	if !isStatsPerfReset {
		_ = os.MkdirAll(cache.Dir(), 0o755)
		_ = metrics.Default().Save(metricsPath)
	}
	os.Exit(code)
}
