// Package graph owns the graph-family tool bodies as plain functions
// over the resolved index. The MCP layer keeps thin adapters that
// resolve the index (and, for the governed variants, build the
// GovContext hook bundle: governor factory + provenance stamping) and
// delegate. Snapshot stays in internal/mcp with its per-action index
// loading.
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/llm"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
	"github.com/JayveerPrajapati/kern/internal/twin"
)

func AstSearch(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	pattern := mcpargs.ArgString(args, "pattern")
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	limit := 50
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	matches := ix.Search(pattern, limit)
	if len(matches) == 0 {
		return "no symbols matched: " + pattern + staleNote(ix), nil
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m.Kind)
		b.WriteString(" ")
		b.WriteString(m.FullName())
		b.WriteString(" ")
		b.WriteString(m.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(m.Line))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n"), nil

}

func EntryPoints(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	limit := 50
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	var b strings.Builder
	n := 0
	// Compile the optional pattern filter once, before the symbol loop.
	var patternRe *regexp.Regexp
	if p := mcpargs.ArgString(args, "pattern"); p != "" {
		var err error
		patternRe, err = regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$")
		if err != nil {
			return "", fmt.Errorf("bad pattern %q: %w", p, err)
		}
	}
	for _, s := range ix.Symbols {
		if !s.Entry || s.Framework == "" {
			continue
		}
		if patternRe != nil {
			if !patternRe.MatchString(s.Name) && (s.Route == "" || !patternRe.MatchString(s.Route)) {
				continue
			}
		}
		fmt.Fprintf(&b, "%s %s %s %s:%d\n", s.Framework, s.FullName(), s.Route, s.File, s.Line)
		n++
		if n >= limit {
			break
		}
	}
	if n == 0 {
		return "no framework entry points in index (run kern build/index to populate)", nil
	}
	return strings.TrimSuffix(b.String(), "\n"), nil

}

func Why(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	info, ok := intel.Why(ix, symbol)
	if !ok {
		return "no symbol found: " + symbol, nil
	}
	if minConf := mcpargs.ArgString(args, "min_confidence"); minConf != "" {
		passes := intel.MinConfidenceFilter(minConf)
		kept := info.Callers[:0]
		for _, c := range info.Callers {
			if passes(intel.EdgeConfidenceLabel(ix, c.Name, info.Symbol.FullName())) {
				kept = append(kept, c)
			}
		}
		info.Callers = kept
		info.InEdges = len(kept)
	}
	return intel.FormatWhy(info), nil

}

func Inherits(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	sym, ok := ix.FindSymbol(symbol)
	if !ok {
		return "no symbol found: " + symbol, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\n", sym.FullName(), sym.Kind)
	sup := ix.SupertypesOf(sym)
	sub := ix.SubtypesOf(sym)
	if len(sup) == 0 && len(sub) == 0 {
		b.WriteString("  no inheritance edges\n")
	}
	for _, s := range sup {
		fmt.Fprintf(&b, "  supertype: %s\n", s)
	}
	for _, s := range sub {
		fmt.Fprintf(&b, "  subtype:   %s\n", s)
	}
	return strings.TrimRight(b.String(), "\n"), nil

}

func Path(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	from := mcpargs.ArgString(args, "from")
	to := mcpargs.ArgString(args, "to")
	if from == "" || to == "" {
		return "", fmt.Errorf("from and to are required")
	}
	from, okFrom := intel.Resolve(ix, from)
	to, okTo := intel.Resolve(ix, to)
	if !okFrom {
		return "", fmt.Errorf("unknown symbol: %s", from)
	}
	if !okTo {
		return "", fmt.Errorf("unknown symbol: %s", to)
	}
	minConf := mcpargs.ArgString(args, "min_confidence")
	return intel.RenderPath(ix, intel.ShortestPathMin(ix, from, to, minConf)), nil

}

func Cycles(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	cycles := intel.ImportCycles(ix)
	if mcpargs.ArgString(args, "json") == "true" {
		b, _ := json.MarshalIndent(map[string]any{"cycles": cycles, "count": len(cycles)}, "", "  ")
		return string(b), nil
	}
	return intel.RenderCycles(cycles), nil
}

func Dead(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	dead := intel.DeadCode(ix)
	limit := 0
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	if limit > 0 && len(dead) > limit {
		dead = dead[:limit]
	}
	return intel.RenderDead(dead), nil

}

func Larges(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	minLines := 60
	if v := mcpargs.ArgString(args, "min_lines"); v != "" {
		n, err := mcpargs.AtoiArg(v, minLines)
		if err != nil {
			return "", err
		}
		minLines = n
	}
	large := intel.LargeFunctions(ix, minLines)
	limit := 0
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	if limit > 0 && len(large) > limit {
		large = large[:limit]
	}
	return intel.RenderLarge(large), nil

}

func Arch(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	return intel.RenderArch(intel.AnalyzeArchitecture(ix)), nil

}

func Surprising(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	limit := 15
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	return intel.RenderSurprising(intel.SurprisingConnections(ix, limit)), nil
}

func Near(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	depth := 2
	if v := mcpargs.ArgString(args, "depth"); v != "" {
		n, err := mcpargs.AtoiArg(v, depth)
		if err != nil {
			return "", err
		}
		depth = n
	}
	maxNodes := 100
	if v := mcpargs.ArgString(args, "max"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxNodes)
		if err != nil {
			return "", err
		}
		maxNodes = n
	}
	nodes, err := intel.Near(ix, symbol, depth, maxNodes)
	if err != nil {
		return "", err
	}
	return intel.RenderNear(ix, nodes), nil

}

func Bridges(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	limit := 15
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	return intel.RenderBridges(intel.Bridges(ix, limit)), nil

}

func Trace(ctx context.Context, ix *index.Index, args map[string]any) (string, error) {
	src := mcpargs.ArgString(args, "trace")
	if src == "" {
		return "", fmt.Errorf("trace is required")
	}
	limit := 0
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	return intel.RenderTrace(intel.Trace(ix, src, "trace", limit)), nil

}

func Frameworks(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	det, err := fw.DetectWithStdlib(root)
	if err != nil {
		return "", err
	}
	return fw.Render(det), nil

}

func FWTrace(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	filter := mcpargs.ArgString(args, "filter")
	format := mcpargs.ArgString(args, "format")

	res, err := fw.TraceRoutes(ctx, root, filter)
	if err != nil {
		return "", err
	}

	if format == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Framework Route & Dependency Flow (%d routes traced) ===\n", res.Total))
	if len(res.Frameworks) > 0 {
		b.WriteString(fmt.Sprintf("Detected Frameworks: %s\n", strings.Join(res.Frameworks, ", ")))
	}
	b.WriteString("\n")

	for i, r := range res.Routes {
		b.WriteString(fmt.Sprintf("[%d] %s %s (%s)\n", i+1, r.Method, r.Path, r.Framework))
		b.WriteString(fmt.Sprintf("    Declared in: %s:%d\n", r.File, r.Line))
		if len(r.Middleware) > 0 {
			b.WriteString(fmt.Sprintf("    Middleware:  %s\n", strings.Join(r.Middleware, " -> ")))
		}
		b.WriteString(fmt.Sprintf("    Handler:     %s\n", r.Handler))
		if len(r.InjectedServices) > 0 {
			b.WriteString(fmt.Sprintf("    Injected DI: %s\n", strings.Join(r.InjectedServices, ", ")))
		}
		if len(r.DBModels) > 0 {
			b.WriteString(fmt.Sprintf("    DB Models:   %s\n", strings.Join(r.DBModels, ", ")))
		}
		b.WriteString("    Pipeline:\n")
		for _, step := range r.Steps {
			b.WriteString(fmt.Sprintf("      -> [%s] %s (%s:%d)\n", step.Stage, step.Symbol, step.File, step.Line))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

func RepoSearch(ctx context.Context, root string, args map[string]any) (string, error) {
	query := mcpargs.ArgString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := 20
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	// N2 follow-up (cross-repo): capture the primary root's staleness
	// BEFORE the repo walk — the walk's LoadOrBuild fallback rebuilds stale
	// per-repo indexes as a side effect, so a post-walk load would read the
	// fresh rebuild and the note could never fire. Best-effort: an
	// unloadable index yields no note (the verdict cannot be derived),
	// never an error. Only the primary root is checked — sub-repo
	// staleness is not covered by this note.
	rootStale := ""
	if ix, err := index.Load(root); err == nil {
		rootStale = staleNote(ix)
	}
	var hits []intel.RepoHit
	sem := mcpargs.ArgString(args, "semantic")
	if sem == "true" || sem == "1" {
		client := llm.NewEmbedder()
		if !client.HasEmbeddingModel() {
			return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
		}
		hits = intel.SemanticSearchReposIn(root, query, limit, client)
	} else {
		hits = intel.SearchReposIn(root, query, limit)
	}
	if len(hits) == 0 {
		return "no symbols matched across repos: " + clipQuery(query) + rootStale, nil
	}
	return intel.FormatRepoHits(hits), nil

}

func Churn(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	from, to := "", ""
	if r := mcpargs.ArgString(args, "range"); r != "" {
		if p := strings.SplitN(r, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			from = r
		}
	}
	report, err := intel.Churn(root, from, to)
	if err != nil {
		return "", err
	}
	return intel.RenderChurn(report), nil

}

func FtsSearch(ctx context.Context, root string, args map[string]any) (string, error) {
	query := mcpargs.ArgString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if !index.SQLiteEnabled() {
		return "", fmt.Errorf("fts5 requires a build with -tags sqlite (rebuild kern with 'go build -tags sqlite'); use kern_search for ranked free-text search on this default build")
	}
	limit := 20
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	// No LoadSQLite precheck here: it loaded the ENTIRE index into memory
	// purely as an existence check, and FTS5Search below re-opens the store
	// and returns a clear error when it is missing.
	matches, err := index.FTS5Search(root, query, limit)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "no full-text matches: " + query, nil
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m.Kind)
		b.WriteString(" ")
		b.WriteString(m.FullName())
		b.WriteString(" ")
		b.WriteString(m.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(m.Line))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n"), nil

}

func Cochange(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	from, to := "", ""
	if r := mcpargs.ArgString(args, "range"); r != "" {
		if p := strings.SplitN(r, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			from = r
		}
	}
	limit := 20
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	report, err := intel.CoChange(root, from, to)
	if err != nil {
		return "", err
	}
	return intel.RenderCoChange(report, limit), nil

}

// clipQuery truncates a search query for echo in output lines (QA F7:
// a 5000-char query was echoed in full inside "no symbols matched").
// 120 chars is enough to recognize any realistic query.
func clipQuery(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[:117] + "..."
}

func Search(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	query := mcpargs.ArgString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := 20
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	var matches []index.Symbol
	sem := mcpargs.ArgString(args, "semantic")
	if sem == "true" || sem == "1" {
		client := llm.NewEmbedder()
		if !client.HasEmbeddingModel() {
			return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
		}
		matches = intel.SemanticSearch(ix, query, limit, client)
	} else {
		matches = intel.RankedSearch(ix, query, limit)
	}
	// Governance: kern_search runs authorization like every other
	// retrieval tool. No agent_id → the default agent + cwd-scoped scope
	// governs; KERN_MCP_PERMISSIVE=1 restores raw mode (nil governor,
	// unfiltered results).
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	if gov != nil {
		var kept []index.Symbol
		for _, m := range matches {
			if gov.Allowed[m.FullName()] {
				kept = append(kept, m)
			}
		}
		matches = kept
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, searchSymbolNames(matches)))
	} else {
		gvc.StampRaw(provenance.SymbolProvenances(ix, searchSymbolNames(matches)))
	}
	if len(matches) == 0 {
		return "no symbols matched: " + clipQuery(query) + staleNote(ix), nil
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m.Kind)
		b.WriteString(" ")
		b.WriteString(m.FullName())
		b.WriteString(" ")
		b.WriteString(m.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(m.Line))
		if ix.IsGenerated(m.File) {
			b.WriteString(" (generated)")
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n"), nil

}

func Context(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	lines := 12
	if v := mcpargs.ArgString(args, "lines"); v != "" {
		n, err := mcpargs.AtoiArg(v, lines)
		if err != nil {
			return "", err
		}
		lines = n
	}
	// The level/handle retrieval paths were removed (surface consolidation
	// T2a): progressive-disclosure level views are served exclusively by
	// kern_retrieve. Reject the args loudly instead of silently ignoring
	// them or falling back to the default context slice.
	if handle := mcpargs.ArgString(args, "handle"); handle != "" {
		return "", fmt.Errorf("level views are served by kern_retrieve (use kern_retrieve with the handle %q)", handle)
	}
	if level := mcpargs.ArgString(args, "level"); level != "" {
		return "", fmt.Errorf("level views are served by kern_retrieve (use kern_retrieve with the handle)")
	}
	gov, err := gvc.NewGov()
	if err != nil {
		// Authorization failure (unknown agent, firewall deny): auditable
		// denial provenance, no symbol content.
		gvc.StampGov(gov, nil)
		return "", err
	}
	body := ix.Context(symbol, lines)
	if gov != nil {
		// The Context footer lists the symbol's callers/callees by name;
		// filter it so no denied name leaks through the source slice.
		body = gov.FilterContextFooter(ix, body)
		def, found := ix.ResolveName(symbol)
		if !found {
			suggestions := ix.Search(symbol, 5)
			if len(suggestions) == 0 {
				suggestions = ix.Search("*"+symbol+"*", 5)
			}
			var names []string
			seen := make(map[string]bool)
			for _, sym := range suggestions {
				fullName := sym.FullName()
				if fullName == "" || seen[fullName] {
					continue
				}
				if !gov.NameAllowed(ix, fullName) {
					continue
				}
				seen[fullName] = true
				names = append(names, fullName)
				if len(names) >= 5 {
					break
				}
			}
			gvc.StampGov(gov, nil)
			if len(names) > 0 {
				return fmt.Sprintf("no symbol found: %s. Did you mean: %s? (Use kern_search for ranked search)%s", symbol, strings.Join(names, ", "), FreshnessFooter(args, ix)), nil
			}
			return "no symbol found: " + symbol + FreshnessFooter(args, ix), nil
		}
		if !gov.NameAllowed(ix, def.FullName()) {
			// Denied: identical non-leaking response (the agent cannot tell
			// "denied" from "does not exist"), governed provenance with an
			// empty symbol set and the authorizing rule.
			gvc.StampGov(gov, nil)
			return "no symbol found: " + symbol, nil
		}
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, []string{def.FullName()}))
	} else {
		syms := []provenance.SymbolProvenance{}
		if def, ok := ix.ResolveName(symbol); ok {
			syms = provenance.SymbolProvenances(ix, []string{def.FullName()})
		}
		gvc.StampRaw(syms)
	}
	// Disambiguation note: several packages can define the same symbol
	// name (e.g. "main"); surface which one this slice came from so a
	// wrong-package resolve is spotted immediately (F-3).
	if def, ok := ix.ResolveName(symbol); ok && body != "" {
		body = fmt.Sprintf("# resolved %s -> %s:%d\n%s", symbol, def.File, def.Line, body)
	}
	if body == "" {
		suggestions := ix.Search(symbol, 5)
		if len(suggestions) == 0 {
			suggestions = ix.Search("*"+symbol+"*", 5)
		}
		var names []string
		seen := make(map[string]bool)
		for _, sym := range suggestions {
			fullName := sym.FullName()
			if fullName == "" || seen[fullName] {
				continue
			}
			if gov != nil && !gov.NameAllowed(ix, fullName) {
				continue
			}
			seen[fullName] = true
			names = append(names, fullName)
			if len(names) >= 5 {
				break
			}
		}
		if len(names) > 0 {
			return fmt.Sprintf("no symbol found: %s. Did you mean: %s? (Use kern_search for ranked search)%s", symbol, strings.Join(names, ", "), FreshnessFooter(args, ix)), nil
		}
		return "no symbol found: " + symbol + FreshnessFooter(args, ix), nil
	}
	if mcpargs.ArgBool(args, "terse_code") || mcpargs.ArgBool(args, "terse") {
		path := symbol + ".go"
		if def, ok := ix.ResolveName(symbol); ok && def.File != "" {
			path = def.File
		}
		body = string(kernctx.PruneCode(path, []byte(body), true))
	}
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		if maxTok, err := mcpargs.AtoiArg(v, 0); err == nil && maxTok > 0 {
			body = budget.FitCode(body, maxTok)
		}
	}
	if lensName := mcpargs.ArgString(args, "lens"); lensName != "" {
		l, err := lenses.Resolve(lensName)
		if err != nil {
			return "", err
		}
		body = fmt.Sprintf("lens: %s (%s)\n", l.Name, lenses.RenderPriorities(l)) + body
	}
	if profileName := mcpargs.ArgString(args, "profile"); profileName != "" {
		p, ok := profiles.NewRegistryWithUserProfiles(ix.Root).Select(profileName)
		if !ok {
			return "", fmt.Errorf("unknown profile %q", profileName)
		}
		body = profiles.ApplyProfile(p, body)
	}
	return body + FreshnessFooter(args, ix), nil
}

func Graph(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	// format=one-line serves the former kern_code_graph contract: the
	// single-line call-graph neighbourhood (definition, callers, callees).
	if mcpargs.ArgString(args, "format") == "one-line" {
		out := ix.Graph(symbol)
		if mcpargs.ArgBool(args, "entities") {
			block, err := entityBlock(ix, args, symbol)
			if err != nil {
				return "", fmt.Errorf("unknown symbol: %s", symbol)
			}
			out += "\n" + block
		}
		return out, nil
	}
	// Absent max_tokens → adaptive default scaled to the symbol's
	// adjacency degree (section-15-item-3); an explicit max_tokens
	// (including "0" = no cap) wins untouched.
	maxTokens := intel.AdaptiveGraphCtxTokens(ix, symbol)
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	out, err := intel.GraphCtxMin(ix, symbol, maxTokens, mcpargs.ArgString(args, "min_confidence"))
	if err != nil {
		if gov != nil {
			gvc.StampGov(gov, nil)
		}
		return "", err
	}
	if gov != nil {
		if resolved, ok := intel.Resolve(ix, symbol); ok && !gov.Allowed[resolved] {
			// Root denied: identical to the unknown-symbol error, so the agent
			// cannot tell "denied" from "does not exist".
			gvc.StampGov(gov, nil)
			return "", fmt.Errorf("unknown symbol: %s", symbol)
		}
		out = gov.FilterGraphText(ix, out)
		gvc.StampGov(gov, mcpgov.GraphSymbolsFromText(ix, out))
	} else {
		gvc.StampRaw(mcpgov.GraphSymbolsFromText(ix, out))
	}
	// entities=true appends the digital-twin entity block: the API
	// endpoints, DB tables, topics, services, and deployments connected to
	// the symbol via twin edges (same deterministic render as `kern graph
	// --entities`). The CLI graph path and this leaf both operate on the
	// code graph, so the twin extractors are merged in exactly the way
	// platform.go wires them (twin.MergeIntoIndex).
	if mcpargs.ArgBool(args, "entities") {
		block, err := entityBlock(ix, args, symbol)
		if err != nil {
			return "", fmt.Errorf("unknown symbol: %s", symbol)
		}
		out += "\n" + block
	}
	return out + FreshnessFooter(args, ix), nil
}

// entityBlock renders the digital-twin entity block for a symbol over the
// twin-merged knowledge graph. The symbol must resolve in the code graph; an
// unresolvable symbol returns an error so the caller surfaces the standard
// unknown-symbol contract.
func entityBlock(ix *index.Index, args map[string]any, symbol string) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = ix.Root
	}
	ents, err := twin.Entities(twin.MergeIntoIndex(ix, root), symbol)
	if err != nil {
		return "", err
	}
	return twin.RenderEntities(ents), nil
}

func Explore(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	symbol := mcpargs.ArgString(args, "symbol")
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	depth := 2
	if v := mcpargs.ArgString(args, "depth"); v != "" {
		n, err := mcpargs.AtoiArg(v, depth)
		if err != nil {
			return "", err
		}
		depth = n
	}
	maxNodes := 30
	if v := mcpargs.ArgString(args, "max"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxNodes)
		if err != nil {
			return "", err
		}
		maxNodes = n
	}
	// The level retrieval path was removed (surface consolidation T2a):
	// progressive-disclosure level views are served exclusively by
	// kern_retrieve. Reject the arg loudly instead of silently falling back.
	if level := mcpargs.ArgString(args, "level"); level != "" {
		return "", fmt.Errorf("level views are served by kern_retrieve (use kern_retrieve with the handle)")
	}
	maxTokens := 0
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	rep, err := intel.ExploreBudgeted(ix, symbol, depth, maxNodes, mcpargs.ArgString(args, "min_confidence"), maxTokens)
	if err != nil {
		// Total miss: surface the strongest ranked candidates as a "did you
		// mean" hint (the CLI does the same) instead of a bare error, so the
		// agent can recover from a typo or case variant. Only hits scoring
		// >= 150 are suggested; weak matches are noise, not hints.
		if sugg := exploreSuggestions(ix, symbol); sugg != "" {
			// Preserve the error chain: the suggestion list is folded into the
			// message while err stays wrapped via %w as the final verb.
			return "", fmt.Errorf("did you mean one of: %s: %w", sugg, err)
		}
		return "", err
	}
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	if gov != nil {
		if !gov.Allowed[rep.Resolved] {
			// Root denied: identical to the unknown-symbol error, so the agent
			// cannot tell "denied" from "does not exist".
			gvc.StampGov(gov, nil)
			return "", fmt.Errorf("unknown symbol: %s", symbol)
		}
		// Filter pre-render, in the handler layer: drop nodes outside the
		// authorized scope and edges touching them, then re-derive the
		// affected files from the filtered radius.
		callers := gov.FilterQualified(ix, ix.CallersFor(rep.Definition), false)
		callees := gov.FilterQualified(ix, ix.CallsFor(rep.Definition), true)
		radius := gov.FilterQualified(ix, rep.BlastRadius, false)
		rep.Callers = mcpgov.SimpleNames(callers)
		rep.Callees = mcpgov.SimpleNames(callees)
		rep.BlastRadius = radius
		rep.BlastFiles = intel.AffectedFiles(ix, radius)
		rep.Source = gov.FilterContextFooter(ix, rep.Source)
		names := append([]string{rep.Resolved}, callers...)
		names = append(names, callees...)
		names = append(names, radius...)
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, names))
	} else {
		names := append([]string{rep.Resolved}, rep.Callers...)
		names = append(names, rep.Callees...)
		names = append(names, rep.BlastRadius...)
		gvc.StampRaw(provenance.SymbolProvenances(ix, names))
	}
	rendered := intel.RenderExplore(rep)
	if mcpargs.ArgString(args, "explain") == "true" {
		if info, ok := intel.Why(ix, symbol); ok {
			if gov != nil {
				// Governed mode: the rationale names callers, so it
				// gets the same scope filter as the report itself.
				names := make([]string, 0, len(info.Callers))
				for _, c := range info.Callers {
					names = append(names, c.Name)
				}
				keep := map[string]bool{}
				for _, n := range gov.FilterQualified(ix, names, false) {
					keep[n] = true
				}
				kept := info.Callers[:0]
				for _, c := range info.Callers {
					if keep[c.Name] {
						kept = append(kept, c)
					}
				}
				info.Callers = kept
				info.InEdges = len(kept)
			}
			rendered = intel.RenderExploreExplain(rep, info)
		}
	}
	return rendered + FreshnessFooter(args, ix), nil
}

// exploreSuggestions builds the "did you mean" hint for a failed
// kern_explore lookup from the strongest ranked-search hits for the queried
// symbol. Only hits scoring >= 150 are suggested (a weak match is noise, not
// a hint) and names are deduped, mirroring the CLI's suggestion set.
func exploreSuggestions(ix *index.Index, symbol string) string {
	hits := intel.RankedSearchScored(ix, symbol, 5)
	seen := map[string]bool{}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		if h.Score < 150 {
			continue
		}
		name := h.Symbol.Name
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func Probe(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	task := mcpargs.ArgString(args, "task")
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	// The level retrieval path was removed (surface consolidation T2a):
	// progressive-disclosure level views are served exclusively by
	// kern_retrieve. Reject the arg loudly instead of silently falling back.
	if level := mcpargs.ArgString(args, "level"); level != "" {
		return "", fmt.Errorf("level views are served by kern_retrieve (use kern_retrieve with the handle)")
	}
	maxTokens := 4000
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	report := intel.Probe(ix, task, maxTokens)
	// min_confidence prunes AMBIGUOUS anchors' call rows so the probe
	// bundle never leads an agent to chase phantom references.
	if minConf := mcpargs.ArgString(args, "min_confidence"); minConf != "" {
		passes := intel.MinConfidenceFilter(minConf)
		for i := range report.Anchors {
			a := &report.Anchors[i]
			keptCallers := a.Callers[:0]
			for _, c := range a.Callers {
				if passes(intel.EdgeConfidenceLabel(ix, c, a.Resolved)) {
					keptCallers = append(keptCallers, c)
				}
			}
			a.Callers = keptCallers
			keptCallees := a.Callees[:0]
			for _, c := range a.Callees {
				if passes(intel.EdgeConfidenceLabel(ix, a.Resolved, c)) {
					keptCallees = append(keptCallees, c)
				}
			}
			a.Callees = keptCallees
		}
	}
	text := intel.RenderProbe(report)
	if report.Truncated {
		text = intel.FitProbe(text, maxTokens)
	}
	return text, nil

}

func Communities(ctx context.Context, ix *index.Index, sess *project.Session, args map[string]any) (string, error) {
	limit := 0
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, 0)
		if err != nil {
			return "", err
		}
		limit = n
	}
	comms := sess.CommunitiesList(ix)
	if limit > 0 && len(comms) > limit {
		comms = comms[:limit]
	}
	return intel.RenderCommunities(comms), nil

}

// staleNote appends an inline staleness warning to a no-match result when
// the serving index is stale (N2): a bare "no symbols matched" reads as a
// confident answer even though recent file changes may not be reflected in
// the index. The verdict reuses index.FreshnessProof — the same source the
// provenance footer renders — never a second computation. Returns "" for
// fresh/unknown indexes so normal results keep their exact output. Callers
// without an index (RepoSearch) have no verdict and get no note.
func staleNote(ix *index.Index) string {
	if ix == nil || ix.FreshnessProof(ix.Root).Verdict != index.FreshnessStale {
		return ""
	}
	age := time.Since(ix.UpdatedAt)
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("\n[kern] note: index is STALE (built %s ago) — recent file changes may not be reflected; retry in a moment to pick up the rebuilt index (the rebuild is asynchronous).", age.Round(time.Second))
}

// freshnessFooter renders the opt-in content-addressed freshness proof footer.
// It is appended ONLY when the caller passes with_freshness=true (or "true"/"1")
// in the tool arguments, so existing agents see byte-identical responses.
func FreshnessFooter(args map[string]any, ix *index.Index) string {
	if !mcpargs.ArgBool(args, "with_freshness") {
		return ""
	}
	data, err := json.Marshal(ix.FreshnessProof(ix.Root))
	if err != nil {
		return ""
	}
	return "\n---freshness-proof---\n" + string(data)
}

// searchSymbolNames extracts the qualified names from a ranked/semantic search
// result set, for provenance and governance filtering.
func searchSymbolNames(matches []index.Symbol) []string {
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.FullName())
	}
	return names
}

// RetrieveItemNames returns the non-empty item names of an L1 result.
func RetrieveItemNames(items []retrieval.L1Item) []string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		if it.Name != "" {
			names = append(names, it.Name)
		}
	}
	return names
}

// SnapshotCreate implements the create action of kern_snapshot: it
// renders the index snapshot (whole-tree, or the subgraph around a
// symbol) as indented JSON. The mcp adapter resolves and loads the
// index.
func SnapshotCreate(ix *index.Index, args map[string]any) (string, error) {
	mode := "whole"
	if sym := mcpargs.ArgString(args, "symbol"); sym != "" {
		mode = "subgraph"
	}
	limit := 0
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	snap, err := ix.Snapshot(mode, mcpargs.ArgString(args, "symbol"), limit)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SnapshotVerify implements the verify action of kern_snapshot: it
// loads a snapshot file and verifies it against the working tree at
// root (resolved by the mcp adapter), returning the verdict as
// indented JSON.
func SnapshotVerify(root string, args map[string]any) (string, error) {
	file := mcpargs.ArgString(args, "file")
	if file == "" {
		return "", fmt.Errorf("snapshot verify: file argument is required")
	}
	snap, err := index.LoadSnapshot(file)
	if err != nil {
		return "", err
	}
	strict := mcpargs.ArgBool(args, "strict")
	verdict, err := index.VerifySnapshot(root, snap, strict)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{
		"verdict":        string(verdict),
		"content_root":   snap.Identity.ContentRoot,
		"built_at":       snap.Identity.BuiltAt,
		"checked_files":  len(snap.Files),
		"tree_oid":       snap.Identity.TreeOID,
		"git_commit":     snap.Identity.GitCommit,
		"schema_version": snap.SchemaVersion,
		"strict":         strict,
	}, "", "  ")
	return string(b), nil
}
