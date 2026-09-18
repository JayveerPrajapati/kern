// Package retrieve owns the retrieve-family tool bodies
// (kern_retrieve, kern_resolve). Both are governed variants: they take
// the resolved index plus the gov.GovContext hook bundle (governor
// factory + provenance stamping) injected by the mcp adapter — the
// same contract as the governed graph family.
package retrieve

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/graph"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
)

// parseRetrieveLevel maps the string form of a disclosure level ("l1"|"l2"|"l3")
// to the retrieval.Level constants. Empty defaults to L2, matching the tool
// schema's default.
func parseRetrieveLevel(v string) (retrieval.Level, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "l2":
		return retrieval.L2, nil
	case "l1":
		return retrieval.L1, nil
	case "l3":
		return retrieval.L3, nil
	}
	return 0, fmt.Errorf("invalid level %q (want l1|l2|l3)", v)
}

// renderWithHandle renders a retrieval result and, for L2/L3 results, appends
// the registered handle line. The L1 render already displays 8-char handle
// prefixes, but the L2/L3 renders omit the handle even though retrieval
// registers one — leaving kern_resolve undrivable from L2/L3 output. Appending
// the handle here restores the documented retrieve→resolve flow at every
// level.
func renderWithHandle(res *retrieval.Result) string {
	out := retrieval.Render(res)
	var h *retrieval.Handle
	switch {
	case res == nil:
		return out
	case res.Detail != nil && res.Detail.Handle != nil:
		h = res.Detail.Handle
	case res.Source != nil && res.Source.Handle != nil:
		h = res.Source.Handle
	}
	if h == nil {
		return out
	}
	id := h.ID
	if len(id) > 8 {
		id = id[:8]
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += fmt.Sprintf("handle %s %s %s:%d (resolve with kern_resolve)\n", id, h.Name, h.Source, h.Line)
	return out
}

func Retrieve(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	query := mcpargs.ArgString(args, "query")
	symbol := mcpargs.ArgString(args, "symbol")
	taskType := mcpargs.ArgString(args, "task_type")
	if taskType != "" && mcpargs.ArgString(args, "level") != "" {
		return "", fmt.Errorf("use only one of level/task_type")
	}
	level, err := parseRetrieveLevel(mcpargs.ArgString(args, "level"))
	if err != nil {
		return "", err
	}
	if taskType != "" {
		// Task-type retrieval: the disclosure level comes from the
		// planner policy for the task type (documentation→l1,
		// refactor→l3, everything else l2).
		if symbol == "" {
			return "", fmt.Errorf("symbol is required for task_type retrieval")
		}
	} else {
		if level == retrieval.L1 && query == "" {
			return "", fmt.Errorf("query is required for level l1")
		}
		if level != retrieval.L1 && symbol == "" {
			return "", fmt.Errorf("symbol is required for level l2/l3")
		}
	}
	limit := 10
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	depth := 0
	if v := mcpargs.ArgString(args, "depth"); v != "" {
		n, err := mcpargs.AtoiArg(v, depth)
		if err != nil {
			return "", err
		}
		depth = n
	}
	maxNodes := 0
	if v := mcpargs.ArgString(args, "max_nodes"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxNodes)
		if err != nil {
			return "", err
		}
		maxNodes = n
	}
	lines := 12
	if v := mcpargs.ArgString(args, "lines"); v != "" {
		n, err := mcpargs.AtoiArg(v, lines)
		if err != nil {
			return "", err
		}
		lines = n
	}
	maxTokens := 0
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	res, err := func() (*retrieval.Result, error) {
		if taskType != "" {
			return retrieval.RetrieveForTask(ix, symbol, taskType, maxTokens)
		}
		return retrieval.Retrieve(ix, retrieval.Options{
			Query:     query,
			Symbol:    symbol,
			Level:     level,
			Limit:     limit,
			Depth:     depth,
			MaxNodes:  maxNodes,
			Lines:     lines,
			MaxTokens: maxTokens,
		})
	}()
	if err != nil {
		return "", err
	}
	// Governance: kern_retrieve runs authorization like every other
	// retrieval tool (mirrors handleSearch). No agent_id → the default
	// agent cwd-scoped scope governs; KERN_MCP_PERMISSIVE=1 restores raw
	// mode (nil governor, unfiltered results).
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	if gov != nil {
		var kept []retrieval.L1Item
		for _, it := range res.Items {
			if gov.Allowed[it.Name] {
				kept = append(kept, it)
			}
		}
		res.Items = kept
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, graph.RetrieveItemNames(res.Items)))
	} else {
		gvc.StampRaw(provenance.SymbolProvenances(ix, graph.RetrieveItemNames(res.Items)))
	}
	return renderWithHandle(res) + graph.FreshnessFooter(args, ix), nil

}

func Resolve(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "handle")
	if id == "" {
		return "", fmt.Errorf("handle is required")
	}
	h, ok := retrieval.DefaultRegistry.Resolve(id)
	if !ok {
		// Render displays an 8-char handle prefix; fall back to a prefix
		// match against the registry so a handle copied verbatim from
		// kern_retrieve output resolves.
		for _, cand := range retrieval.DefaultRegistry.List() {
			if strings.HasPrefix(cand.ID, id) {
				h = cand
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", fmt.Errorf("unknown handle %q (handles expire with the registry; re-run kern_retrieve)", id)
	}
	level, err := parseRetrieveLevel(mcpargs.ArgString(args, "level"))
	if err != nil {
		return "", err
	}
	maxTokens := 0
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	res, err := retrieval.Retrieve(ix, retrieval.Options{
		Symbol:    h.Name,
		Level:     level,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", err
	}
	// Governance: mirror handleSearch exactly — authorization runs
	// like every other retrieval tool; the resolved handle's symbol is the
	// governed unit.
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	if gov != nil {
		var kept []retrieval.L1Item
		for _, it := range res.Items {
			if gov.Allowed[it.Name] {
				kept = append(kept, it)
			}
		}
		res.Items = kept
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, graph.RetrieveItemNames(res.Items)))
	} else {
		gvc.StampRaw(provenance.SymbolProvenances(ix, graph.RetrieveItemNames(res.Items)))
	}
	return renderWithHandle(res) + graph.FreshnessFooter(args, ix), nil

}
