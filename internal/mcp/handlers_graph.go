package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func (s *Server) handleAstSearch(ctx context.Context, args map[string]any) (string, error) {
	{
		pattern := argString(args, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		matches := ix.Search(pattern, limit)
		if len(matches) == 0 {
			return "no symbols matched: " + pattern, nil
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
}

func (s *Server) handleFrameworks(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		det, err := fw.Detect(root)
		if err != nil {
			return "", err
		}
		return fw.Render(det), nil

	}
}

func (s *Server) handleEntryPoints(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var b strings.Builder
		n := 0
		for _, s := range ix.Symbols {
			if !s.Entry || s.Framework == "" {
				continue
			}
			if p := argString(args, "pattern"); p != "" {
				re, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$")
				if err != nil {
					return "", fmt.Errorf("bad pattern %q: %w", p, err)
				}
				if !re.MatchString(s.Name) && (s.Route == "" || !re.MatchString(s.Route)) {
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
}

func (s *Server) handleSearch(ctx context.Context, args map[string]any) (string, error) {
	{
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var matches []index.Symbol
		sem := argString(args, "semantic")
		if sem == "true" || sem == "1" {
			client := llm.NewEmbedder()
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			matches = intel.SemanticSearch(ix, query, limit, client)
		} else {
			matches = intel.RankedSearch(ix, query, limit)
		}
		// Governance (P0.1): kern_search runs authorization like every other
		// retrieval tool. No agent_id → the default agent + cwd-scoped scope
		// governs; KERN_MCP_PERMISSIVE=1 restores raw mode (nil governor,
		// unfiltered results).
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		if gov != nil {
			var kept []index.Symbol
			for _, m := range matches {
				if gov.allowed[m.FullName()] {
					kept = append(kept, m)
				}
			}
			matches = kept
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, symbolProvenances(ix, searchSymbolNames(matches))))
		} else {
			s.stampProvenance(ctx, s.rawProvenance(ix, symbolProvenances(ix, searchSymbolNames(matches))))
		}
		if len(matches) == 0 {
			return "no symbols matched: " + query, nil
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

func (s *Server) handleRepoSearch(ctx context.Context, args map[string]any) (string, error) {
	{
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var hits []intel.RepoHit
		sem := argString(args, "semantic")
		if sem == "true" || sem == "1" {
			client := llm.NewEmbedder()
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			hits = intel.SemanticSearchRepos(query, limit, client)
		} else {
			hits = intel.SearchRepos(query, limit)
		}
		if len(hits) == 0 {
			return "no symbols matched across registered repos: " + query, nil
		}
		return intel.FormatRepoHits(hits), nil

	}
}

func (s *Server) handleWhy(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		info, ok := intel.Why(ix, symbol)
		if !ok {
			return "no symbol found: " + symbol, nil
		}
		return intel.FormatWhy(info), nil

	}
}

func (s *Server) handleCodeGraph(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		return ix.Graph(symbol), nil

	}
}

func (s *Server) handleInherits(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
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
}

// freshnessFooter renders the opt-in content-addressed freshness proof footer.
// It is appended ONLY when the caller passes with_freshness=true (or "true"/"1")
// in the tool arguments, so existing agents see byte-identical responses.
func (s *Server) freshnessFooter(args map[string]any, ix *index.Index) string {
	if !argBool(args, "with_freshness") {
		return ""
	}
	data, err := json.Marshal(ix.FreshnessProof(ix.Root))
	if err != nil {
		return ""
	}
	return "\n---freshness-proof---\n" + string(data)
}

func (s *Server) handleContext(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		lines := 12
		if v := argString(args, "lines"); v != "" {
			n, err := atoiArg(v, lines)
			if err != nil {
				return "", err
			}
			lines = n
		}
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			// Authorization failure (unknown agent, firewall deny): auditable
			// denial provenance, no symbol content.
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		body := ix.Context(symbol, lines)
		if gov != nil {
			// The Context footer lists the symbol's callers/callees by name;
			// filter it so no denied name leaks through the source slice.
			body = gov.filterContextFooter(ix, body)
			def, found := ix.ResolveName(symbol)
			if !found || !gov.nameAllowed(ix, def.FullName()) {
				// Denied or absent — identical non-leaking response (the agent
				// cannot tell "denied" from "does not exist"), governed
				// provenance with an empty symbol set and the authorizing rule.
				s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
				return "no symbol found: " + symbol, nil
			}
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, symbolProvenances(ix, []string{def.FullName()})))
		} else {
			syms := []SymbolProvenance{}
			if def, ok := ix.ResolveName(symbol); ok {
				syms = symbolProvenances(ix, []string{def.FullName()})
			}
			s.stampProvenance(ctx, s.rawProvenance(ix, syms))
		}
		if body == "" {
			return "no symbol found: " + symbol + s.freshnessFooter(args, ix), nil
		}
		return body + s.freshnessFooter(args, ix), nil
	}
}
func (s *Server) handlePath(ctx context.Context, args map[string]any) (string, error) {
	{
		from := argString(args, "from")
		to := argString(args, "to")
		if from == "" || to == "" {
			return "", fmt.Errorf("from and to are required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		from, okFrom := intel.Resolve(ix, from)
		to, okTo := intel.Resolve(ix, to)
		if !okFrom {
			return "", fmt.Errorf("unknown symbol: %s", from)
		}
		if !okTo {
			return "", fmt.Errorf("unknown symbol: %s", to)
		}
		return intel.RenderPath(ix, intel.ShortestPath(ix, from, to)), nil

	}
}

func (s *Server) handleDead(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		dead := intel.DeadCode(ix)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
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
}

func (s *Server) handleLarges(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		minLines := 60
		if v := argString(args, "min_lines"); v != "" {
			n, err := atoiArg(v, minLines)
			if err != nil {
				return "", err
			}
			minLines = n
		}
		large := intel.LargeFunctions(ix, minLines)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
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
}

func (s *Server) handleArch(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		return intel.RenderArch(intel.AnalyzeArchitecture(ix)), nil

	}
}

func (s *Server) handleCommunities(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, 0)
			if err != nil {
				return "", err
			}
			limit = n
		}
		sess := s.sessionFor(argString(args, "root"))
		comms := sess.CommunitiesList(ix)
		if limit > 0 && len(comms) > limit {
			comms = comms[:limit]
		}
		return intel.RenderCommunities(comms), nil

	}
}

func (s *Server) handleChurn(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		from, to := "", ""
		if r := argString(args, "range"); r != "" {
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
}

func (s *Server) handleNear(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		depth := 2
		if v := argString(args, "depth"); v != "" {
			n, err := atoiArg(v, depth)
			if err != nil {
				return "", err
			}
			depth = n
		}
		maxNodes := 100
		if v := argString(args, "max"); v != "" {
			n, err := atoiArg(v, maxNodes)
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
}

func (s *Server) handleGraph(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := intel.GraphCtxDefaultTokens
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		out, err := intel.GraphCtx(ix, symbol, maxTokens)
		if err != nil {
			if gov != nil {
				s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			}
			return "", err
		}
		if gov != nil {
			if resolved, ok := intel.Resolve(ix, symbol); ok && !gov.allowed[resolved] {
				// Root denied: identical to the unknown-symbol error, so the agent
				// cannot tell "denied" from "does not exist".
				s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
				return "", fmt.Errorf("unknown symbol: %s", symbol)
			}
			out = gov.filterGraphText(ix, out)
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, graphSymbolsFromText(ix, out)))
		} else {
			s.stampProvenance(ctx, s.rawProvenance(ix, graphSymbolsFromText(ix, out)))
		}
		return out + s.freshnessFooter(args, ix), nil
	}
}
func (s *Server) handleExplore(ctx context.Context, args map[string]any) (string, error) {
	{
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		depth := 0
		if v := argString(args, "depth"); v != "" {
			n, err := atoiArg(v, depth)
			if err != nil {
				return "", err
			}
			depth = n
		}
		maxNodes := 0
		if v := argString(args, "max"); v != "" {
			n, err := atoiArg(v, maxNodes)
			if err != nil {
				return "", err
			}
			maxNodes = n
		}
		rep, err := intel.Explore(ix, symbol, depth, maxNodes)
		if err != nil {
			return "", err
		}
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		if gov != nil {
			if !gov.allowed[rep.Resolved] {
				// Root denied: identical to the unknown-symbol error, so the agent
				// cannot tell "denied" from "does not exist".
				s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
				return "", fmt.Errorf("unknown symbol: %s", symbol)
			}
			// Filter pre-render, in the handler layer: drop nodes outside the
			// authorized scope and edges touching them, then re-derive the
			// affected files from the filtered radius.
			callers := gov.filterQualified(ix, ix.CallersFor(rep.Definition), false)
			callees := gov.filterQualified(ix, ix.CallsFor(rep.Definition), true)
			radius := gov.filterQualified(ix, rep.BlastRadius, false)
			rep.Callers = simpleNames(callers)
			rep.Callees = simpleNames(callees)
			rep.BlastRadius = radius
			rep.BlastFiles = intel.AffectedFiles(ix, radius)
			rep.Source = gov.filterContextFooter(ix, rep.Source)
			names := append([]string{rep.Resolved}, callers...)
			names = append(names, callees...)
			names = append(names, radius...)
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, symbolProvenances(ix, names)))
		} else {
			names := append([]string{rep.Resolved}, rep.Callers...)
			names = append(names, rep.Callees...)
			names = append(names, rep.BlastRadius...)
			s.stampProvenance(ctx, s.rawProvenance(ix, symbolProvenances(ix, names)))
		}
		return intel.RenderExplore(rep) + s.freshnessFooter(args, ix), nil
	}
}
func (s *Server) handleFtsSearch(ctx context.Context, args map[string]any) (string, error) {
	{
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		root := resolveRoot(argString(args, "root"))
		if !index.SQLiteEnabled() {
			return "", fmt.Errorf("FTS5 requires a build with -tags sqlite (rebuild kern with 'go build -tags sqlite'); use kern_search for ranked free-text search on this default build")
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		if _, err := index.LoadSQLite(root); err != nil {
			return "", err
		}
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
}

func (s *Server) handleBridges(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 15
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		return intel.RenderBridges(intel.Bridges(ix, limit)), nil

	}
}

func (s *Server) handleCochange(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		from, to := "", ""
		if r := argString(args, "range"); r != "" {
			if p := strings.SplitN(r, "..", 2); len(p) == 2 {
				from, to = p[0], p[1]
			} else {
				from = r
			}
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
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
}

func (s *Server) handleProbe(ctx context.Context, args map[string]any) (string, error) {
	{
		task := argString(args, "task")
		if task == "" {
			return "", fmt.Errorf("task is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		report := intel.Probe(ix, task, maxTokens)
		text := intel.RenderProbe(report)
		if report.Truncated {
			text = intel.FitProbe(text, maxTokens)
		}
		return text, nil

	}
}

func (s *Server) handleTrace(ctx context.Context, args map[string]any) (string, error) {
	{
		src := argString(args, "trace")
		if src == "" {
			return "", fmt.Errorf("trace is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		return intel.RenderTrace(intel.Trace(ix, src, "trace", limit)), nil

	}
}
