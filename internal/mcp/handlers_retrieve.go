package mcp

import (
	"context"
	"fmt"
	"strings"

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

// retrieveItemNames extracts the qualified symbol names from L1 items, for
// provenance and governance filtering (mirrors searchSymbolNames).
func retrieveItemNames(items []retrieval.L1Item) []string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		if it.Name != "" {
			names = append(names, it.Name)
		}
	}
	return names
}

func (s *Server) handleRetrieve(ctx context.Context, args map[string]any) (string, error) {
	{
		query := argString(args, "query")
		symbol := argString(args, "symbol")
		taskType := argString(args, "task_type")
		if taskType != "" && argString(args, "level") != "" {
			return "", fmt.Errorf("use only one of level/task_type")
		}
		level, err := parseRetrieveLevel(argString(args, "level"))
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
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
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
		if v := argString(args, "max_nodes"); v != "" {
			n, err := atoiArg(v, maxNodes)
			if err != nil {
				return "", err
			}
			maxNodes = n
		}
		lines := 12
		if v := argString(args, "lines"); v != "" {
			n, err := atoiArg(v, lines)
			if err != nil {
				return "", err
			}
			lines = n
		}
		maxTokens := 0
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
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
		// Governance (P0.1): kern_retrieve runs authorization like every other
		// retrieval tool (mirrors handleSearch). No agent_id → the default
		// agent cwd-scoped scope governs; KERN_MCP_PERMISSIVE=1 restores raw
		// mode (nil governor, unfiltered results).
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		if gov != nil {
			var kept []retrieval.L1Item
			for _, it := range res.Items {
				if gov.allowed[it.Name] {
					kept = append(kept, it)
				}
			}
			res.Items = kept
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, symbolProvenances(ix, retrieveItemNames(res.Items))))
		} else {
			s.stampProvenance(ctx, s.rawProvenance(ix, symbolProvenances(ix, retrieveItemNames(res.Items))))
		}
		return retrieval.Render(res) + s.freshnessFooter(args, ix), nil

	}
}

func (s *Server) handleResolve(ctx context.Context, args map[string]any) (string, error) {
	{
		id := argString(args, "handle")
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
		level, err := parseRetrieveLevel(argString(args, "level"))
		if err != nil {
			return "", err
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := 0
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
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
		// Governance (P0.1): mirror handleSearch exactly — authorization runs
		// like every other retrieval tool; the resolved handle's symbol is the
		// governed unit.
		gov, err := s.newGovernor(ctx, args, ix)
		if err != nil {
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, nil))
			return "", err
		}
		if gov != nil {
			var kept []retrieval.L1Item
			for _, it := range res.Items {
				if gov.allowed[it.Name] {
					kept = append(kept, it)
				}
			}
			res.Items = kept
			s.stampProvenance(ctx, s.governedProvenance(ix, gov.policySource, gov.proof, symbolProvenances(ix, retrieveItemNames(res.Items))))
		} else {
			s.stampProvenance(ctx, s.rawProvenance(ix, symbolProvenances(ix, retrieveItemNames(res.Items))))
		}
		return retrieval.Render(res) + s.freshnessFooter(args, ix), nil

	}
}
