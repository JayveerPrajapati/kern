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

// commandCount returns the number of non-alias commands in commandTable, for
// the banner's "specialized micro-commands" count. Alias spellings (e.g. the
// snake_case MCP-mirror duplicates) still dispatch but are not counted, so
// the banner cannot overstate the catalog size (N4).
func commandCount() int {
	n := 0
	for _, e := range commandTable {
		if !e.alias {
			n++
		}
	}
	return n
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
  kern setup [--detect] [--global] [--global-rules] [--agents-md=thin|full]   Wire kern-first tools into AI agents (MCP/OpenCode/Claude)
  kern doctor [root] [--json]                     Diagnostic report (binary, index, wiring, freshness)

Server & Catalog Surfaces:
  kern web [--addr :8090] [--enterprise]          Web console + REST API (loopback-only; the kern-server binary)
  kern mcp tools [category] [--json]              Full MCP tool catalog listing (grouped by category, with risk)
  kern evidence export|verify|explain             Tamper-evident evidence bundles (governance-grade)

`)
	// The command count is derived from commandTable so the banner can never
	// drift from the registered command set (it previously said "140+" while
	// 200+ commands were registered). Alias spellings are excluded from the
	// count (see commandCount).
	fmt.Fprintf(os.Stderr, "Tip: Run 'kern --all' or 'kern help --all' to list all %d specialized micro-commands.\n", commandCount())
	fmt.Fprintln(os.Stderr, "Docs: open docs/index.md (site index) or run 'kern docs <query>' for local doc search (searches only indexed repo docs — for repos with a docs tree, run 'kern docs index <root>' first).")
}

// cliCategoryOrder is the fixed display order for the grouped `kern --all`
// listing and `kern help <category>`. Categories added in the future that are
// not listed here sort to the end of the grouped listing (never dropped).
var cliCategoryOrder = []string{
	"meta", "wiring", "compression", "prompt", "exec", "analysis",
	"autonomy", "governance", "security", "verification", "docs", "search",
	"graph", "framework", "refactor", "memory", "evidence", "context",
	"review", "servers", "locks", "git", "docgen", "snapshot", "mcp-mirror",
}

// commandDescription derives the one-line description for a command entry:
// its help text when present, otherwise the first line of its usage block.
func commandDescription(e commandEntry, name string) string {
	desc := e.help
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
	return desc
}

// aliasNote extracts the "(alias of ...)" note from a usage string, e.g.
// `usage: kern -v [flags]  (alias of version)` → `(alias of version)`. It
// returns "" when the usage carries no alias marker, so the `kern --all`
// listing can append the note without inventing text.
func aliasNote(usage string) string {
	if i := strings.Index(usage, "(alias of "); i >= 0 {
		if j := strings.IndexByte(usage[i:], ')'); j >= 0 {
			return usage[i : i+j+1]
		}
	}
	return ""
}

// usageAll prints the complete CLI catalog. By default it groups commands
// under category headers (fixed order, alphabetical within a category);
// `--flat` keeps the legacy flat listing.
func usageAll(flat bool) {
	// Generated from commandTable so the catalog can never drift from the
	// registered command set (kern --all previously listed only ~100 of
	// 203 registered commands). Help text one-liner when present, otherwise
	// the usage first line.
	fmt.Fprintf(os.Stderr, "kern - kern your context. Complete CLI catalog.\n\nUsage:\n")
	if flat {
		names := slices.Sorted(maps.Keys(commandTable))
		for _, name := range names {
			e := commandTable[name]
			// Alias spellings still dispatch but are skipped from the printed
			// catalog so duplicate kebab/snake pairs stop cluttering help (N4).
			if e.alias {
				continue
			}
			line := "  kern " + name
			if desc := commandDescription(e, name); desc != "" {
				line += "  " + desc
			}
			// Alias entries whose help text is empty derive their
			// description from the usage line and already carry the
			// "(alias of ...)" note — skip the duplicate append then.
			if note := aliasNote(e.usage); note != "" && !strings.Contains(line, "(alias of ") {
				line += "  " + note
			}
			fmt.Fprintln(os.Stderr, line)
		}
		return
	}
	// Grouped: category header line, then its commands alphabetically.
	byCat := map[string][]string{}
	for name, e := range commandTable {
		cat := e.category
		if cat == "" {
			cat = "misc" // never empty in practice; TestEveryCommandHasCategory gates it
		}
		byCat[cat] = append(byCat[cat], name)
	}
	for _, names := range byCat {
		slices.Sort(names)
	}
	seen := map[string]bool{}
	emit := func(cat string) {
		if seen[cat] {
			return
		}
		seen[cat] = true
		names := byCat[cat]
		if len(names) == 0 {
			return
		}
		fmt.Fprintf(os.Stderr, "[%s]\n", cat)
		for _, name := range names {
			e := commandTable[name]
			// Same alias skip as the flat listing: aliases dispatch but are
			// not listed (N4).
			if e.alias {
				continue
			}
			line := "  kern " + name
			if desc := commandDescription(e, name); desc != "" {
				line += "  " + desc
			}
			// Alias entries whose help text is empty derive their
			// description from the usage line and already carry the
			// "(alias of ...)" note — skip the duplicate append then.
			if note := aliasNote(e.usage); note != "" && !strings.Contains(line, "(alias of ") {
				line += "  " + note
			}
			fmt.Fprintln(os.Stderr, line)
		}
	}
	for _, cat := range cliCategoryOrder {
		emit(cat)
	}
	leftovers := slices.Sorted(maps.Keys(byCat))
	for _, cat := range leftovers {
		emit(cat)
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
