// context.go implements the `kern context` CLI entry point: flag parsing,
// symbol resolution and the etag/lens/profile/lines rendering pipeline live
// here; the index context slice comes from internal/context and the
// conditional-fetch etag contract from internal/clietag.
package contextcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/clietag"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/clihints"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/code"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// contextFlags are the flags `kern context` consumes. The set matches the
// dispatch commandFlags["context"] row (etag/lens/lines/profile/root) plus
// --json and --terse-code, which the moved runContext body genuinely reads
// (the F10 dispatch gate rejects them for `kern context` today, but the
// handler paths are preserved verbatim for direct callers).
type contextFlags struct {
	etag      string
	lens      string
	lines     int
	profile   string
	root      string
	json      bool
	jsonSet   bool
	terseCode bool
	mode      string
}

// parseContextFlags replicates the unified cmd/kern parser's semantics for
// the context flags, via the shared cliflags.Parser (single-dash long forms
// normalize, --flag=value and --flag value both work, a trailing value-less
// flag is accept-and-default, unknown flags are a sticky parse error).
// --lines and --line are both accepted (the shared parser's dual case); the
// int error message names "--line" exactly like the shared parser.
func parseContextFlags(args []string) (contextFlags, []string, error) {
	var f contextFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--etag":
			p.SetStr(&f.etag, inline, hasInline)
		case "--lens":
			p.SetStr(&f.lens, inline, hasInline)
		case "--lines", "--line":
			p.SetInt(&f.lines, "--line", inline, hasInline)
		case "--profile":
			p.SetStr(&f.profile, inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
			f.jsonSet = true
		case "--terse-code":
			p.SetBool(&f.terseCode, "--terse-code", inline, hasInline)
		case "--mode":
			p.SetStr(&f.mode, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunContext executes `kern context <symbol> [root] [--lines N] [--etag H]`
// and returns the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunContext(rest []string) int {
	return run(func() int { runContextCLI(rest); return 0 })
}

// contextSummaryFor builds the count-only concise summary (no evidence
// slice, no explore report). It is a package variable so the fallback tests
// can force the summary path to fail deterministically.
var contextSummaryFor = intel.ContextSummaryFor

func runContextCLI(rest []string) {
	f, args, err := parseContextFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	mode, merr := cliflags.ParseOutputMode(f.mode)
	if merr != nil {
		fatalUsage("context: %v", merr)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern context <symbol> [root] [--lines N] [--etag H] [--mode concise|evidence|full]")
	}
	symbol := args[0]
	origSymbol := symbol
	// Empty/whitespace symbols are usage errors, not no-match results —
	// MCP rejects symbol:"" with "symbol is required" (blind-critique
	// Phase 3.3 CLI/MCP alignment).
	if strings.TrimSpace(symbol) == "" {
		fatalUsage("symbol is required (e.g. kern context RunServer)")
	}
	root := rootOf(f.root)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Context: %v", err)
	}
	// Concise fast path (post-optimization Phase 2): the default output only
	// needs counts + risk, so build a summary and skip the source/evidence
	// slice and the full explore report. Only the plain concise shape
	// qualifies — JSON/lens/profile/terse-code/etag all shape the evidence
	// and keep the full path. Any summary error falls through silently to the
	// existing path below, preserving its errors and did-you-mean output.
	if mode == cliflags.ModeConcise && !f.jsonSet && f.lens == "" && f.profile == "" && !f.terseCode && f.etag == "" {
		if summary, serr := contextSummaryFor(ix, origSymbol); serr == nil && summary != nil {
			clietag.PrintIndexStamp(ix)
			fmt.Println(renderContextConciseSummary(summary, origSymbol))
			return
		}
	}
	// Symbol resolution (F12): resolve like `kern what-if` — an exact index
	// hit wins, a unique strong fuzzy match auto-resolves (e.g.
	// `context enqueue` -> Bus.enqueueDeadLetter), and ambiguity or no match
	// keeps the did-you-mean / clear-error path below. Output rendering
	// (tokens, slices, lens, profile) is unchanged — only the symbol used.
	if resolved, ok := resolveContextSymbol(ix, symbol); ok {
		symbol = resolved
	}
	// Honor --lines instead of parsing it as the symbol.
	lines := f.lines
	if lines <= 0 {
		lines = 12
	}
	ctxText := ix.Context(symbol, lines)
	if ctxText == "" {
		fatalNoSymbol(symbol, ix)
	}
	// Disambiguation note (F-3, mirrors the kern_context MCP tool): several
	// packages can define the same symbol name (e.g. "main"); surface which
	// one this slice came from so a wrong-package resolve is spotted
	// immediately instead of silently reading the wrong package.
	if def, ok := ix.ResolveName(symbol); ok {
		ctxText = fmt.Sprintf("# resolved %s -> %s:%d\n%s", symbol, cliout.DisplayPath(def.File), def.Line, ctxText)
	}
	if f.terseCode {
		path := symbol + ".go"
		if def, ok := ix.ResolveName(symbol); ok && def.File != "" {
			path = def.File
		}
		ctxText = string(kernctx.PruneCode(path, []byte(ctxText), true))
	}
	// Review lens (mirrors the kern_context MCP tool): the lens is a HEADER
	// ANNOTATION, not a re-ranking — it prepends the evidence-priority line
	// so the caller knows which review posture the context is sized for. No
	// lens arg -> output unchanged.
	if f.lens != "" {
		l, err := lenses.Resolve(f.lens)
		if err != nil {
			fatal("%v", err)
		}
		ctxText = fmt.Sprintf("lens: %s (%s)\n", l.Name, lenses.RenderPriorities(l)) + ctxText
	}
	// --json is honored (F-CX1): it was previously accepted by the shared
	// parser and silently ignored — identical text with and without it.
	if f.json {
		out := map[string]any{"symbol": symbol, "context": ctxText}
		if def, ok := ix.ResolveName(symbol); ok {
			out["file"] = def.File
			out["line"] = def.Line
		}
		printJSON(out)
		return
	}
	// --profile shapes how the context is presented without changing the
	// evidence (deterministic, no LLM). Applied after the lens header,
	// matching the MCP ordering.
	if f.profile != "" {
		p, ok := profiles.NewRegistryWithUserProfiles(root).Select(f.profile)
		if !ok {
			fatal("unknown profile %q", f.profile)
		}
		ctxText = profiles.ApplyProfile(p, ctxText)
	}
	// Concise mode is the new default: counts + risk only. lens/profile/
	// terse-code shape the full evidence, so their presence keeps evidence
	// mode (a set flag is never silently dropped).
	if mode == cliflags.ModeConcise && f.lens == "" && f.profile == "" && !f.terseCode {
		if concise, ok := conciseContext(ix, origSymbol, symbol, ctxText); ok {
			clietag.PrintIndexStamp(ix)
			if clietag.EtagResponse("kern_context", f.etag, concise) {
				return
			}
			fmt.Println(concise)
			return
		}
	}
	// Conditional fetch (B1, ADR-0012): --etag matches the fresh etag →
	// print `unchanged (etag <E>)` and exit 0; otherwise print the full
	// context and an `etag: <hash>` footer on stderr. The index freshness
	// stamp (3.2-C) precedes it on stderr.
	clietag.PrintIndexStamp(ix)
	if clietag.EtagResponse("kern_context", f.etag, ctxText) {
		return
	}
	fmt.Println(ctxText)
	if def, ok := ix.ResolveName(symbol); ok && def.File != "" {
		filePath := def.File
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(root, filePath)
		}
		if fileContent, rerr := code.ReadFile(filePath); rerr == nil {
			before := tokenize.Count(string(fileContent))
			after := tokenize.Count(ctxText)
			cliout.PrintSavingsFooter(os.Stderr, before, after, kernctx.CostPerToken(), fmt.Sprintf("file read raw (%s)", def.File))
		}
	}
}

// conciseContext builds the concise `kern context` block (counts + risk) from
// an explore report, prepending a resolved note when the user's input
// resolved to a different symbol. ok=false means the report could not be
// built (the caller then falls back to the full evidence slice).
func conciseContext(ix *index.Index, orig, resolved, evidence string) (string, bool) {
	depth, maxN := intel.DefaultExploreBounds(-1, 0)
	rep, err := intel.ExploreBudgeted(ix, resolved, depth, maxN, "", 0)
	if err != nil {
		return "", false
	}
	out := intel.RenderExploreConcise(rep)
	if orig != "" && orig != resolved {
		out = fmt.Sprintf("# resolved %s -> %s\n%s", orig, resolved, out)
	}
	if footer := intel.TokenFooter(evidence, out); footer != "" {
		out += "\n" + footer
	}
	return out, true
}

// renderContextConciseSummary renders the fast-path concise block from a
// count-only summary. After the optional resolved note the lines are
// byte-identical to intel.RenderExploreConcise for the same data. It
// deliberately prints no token footer: the context slice it used to compare
// against is never built on this path, so omitting the footer is the honest
// default.
func renderContextConciseSummary(s *intel.ContextSummary, orig string) string {
	if s == nil {
		return ""
	}
	name := s.Resolved
	if name == "" {
		name = s.Definition.FullName()
	}
	var b strings.Builder
	if orig != "" && orig != name {
		fmt.Fprintf(&b, "# resolved %s -> %s\n", orig, name)
	}
	fmt.Fprintf(&b, "# %s (%s) — %s:%d", name, s.Definition.Kind, s.Definition.File, s.Definition.Line)
	if s.Definition.End > s.Definition.Line {
		fmt.Fprintf(&b, "-%d", s.Definition.End)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "callers: %d\n", s.Callers)
	fmt.Fprintf(&b, "callees: %d\n", s.Callees)
	fmt.Fprintf(&b, "blast: %d symbols / %d files\n", s.Blast, s.Files)
	fmt.Fprintf(&b, "risk: %s", s.Risk)
	return b.String()
}

// resolveContextSymbol resolves the `kern context` symbol argument with the
// same policy as `kern what-if`: an exact index hit wins; a
// single strong fuzzy match (ranked-search score >= 150, the what-if
// threshold) auto-resolves to the matched symbol; multiple distinct strong
// candidates are ambiguous and return false so the caller's did-you-mean
// path renders; nothing matching also returns false (clear error). The
// boolean reports whether the returned name is usable.
func resolveContextSymbol(ix *index.Index, symbol string) (string, bool) {
	if ix == nil || symbol == "" {
		return symbol, false
	}
	// Exact match (same match set as ix.Context: symbolsFor then
	// resolveName, without the file I/O the slice rendering performs).
	if _, ok := ix.FindSymbol(symbol); ok {
		return symbol, true
	}
	if _, ok := ix.ResolveName(symbol); ok {
		return symbol, true
	}
	// Fuzzy fallback (what-if policy): ranked search with the strong-match
	// threshold of 150 (matches every query token), deduped by full name.
	// Exactly one distinct strong candidate auto-resolves; more than one is
	// ambiguous (did-you-mean renders); none is a clear miss.
	var strong []string
	seen := map[string]bool{}
	for _, h := range intel.RankedSearchScored(ix, symbol, 5) {
		if h.Score < 150 {
			continue
		}
		name := h.Symbol.FullName()
		if name == "" {
			name = h.Symbol.Name
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		strong = append(strong, name)
	}
	if len(strong) == 1 {
		return strong[0], true
	}
	return symbol, false
}

// fatalNoSymbol prints a "no symbol found" error with optional suggestions.
func fatalNoSymbol(symbol string, ix *index.Index) {
	fatal("%s", clihints.NoSymbolMsg(symbol, ix))
}

// suggestSymbols returns up to 5 symbol names from ix that are similar to
// query — the local alias for the shared clihints.Suggest (consolidation
// tranche 8), kept so the context-family callers keep working unchanged.
func suggestSymbols(ix *index.Index, query string) []string {
	return clihints.Suggest(ix, query)
}
