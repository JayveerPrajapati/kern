package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// handleHealth returns a structured real-time health snapshot of the kern MCP server,
// allowing AI agents to self-diagnose server state, index freshness, cache hit-rate,
// audit chain length, and active operations without guessing or running expensive retries.
func (s *Server) handleHealth(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		roots := s.workspaceRoots()
		if len(roots) > 0 {
			root = roots[0]
		}
	}
	snap := metrics.Default().Snapshot()

	// Index health & freshness
	indexAge := "unknown"
	indexSymbols := 0
	indexFiles := 0
	indexFresh := false
	indexReused := 0
	indexLowPromoted := 0
	indexLowUnresolved := 0
	if root != "" {
		sess := s.sessionFor(root)
		if sess != nil {
			if ix, ok := sess.CachedIndex(); ok && ix != nil {
				indexFresh = true
				if !ix.UpdatedAt.IsZero() {
					indexAge = time.Since(ix.UpdatedAt).Round(time.Second).String()
				}
				indexSymbols = len(ix.Symbols)
				indexFiles = len(ix.FileHashes)
				indexReused = ix.ReusedResults()
				indexLowPromoted = ix.PromotedLowEdges
				indexLowUnresolved = ix.UnresolvedLowEdges
			}
		}
	}

	// Audit chain length
	auditLen := 0
	s.auditMu.Lock()
	if s.audit != nil {
		auditLen = s.audit.Len()
	}
	s.auditMu.Unlock()

	// Cache performance
	hitRatePct := 0.0
	totalCache := snap.CacheHits + snap.CacheMisses
	if totalCache > 0 {
		hitRatePct = (float64(snap.CacheHits) / float64(totalCache)) * 100
	}

	result := map[string]any{
		"status":  "ok",
		"version": serverVersion,
		"index": map[string]any{
			"root":                 root,
			"fresh":                indexFresh,
			"age":                  indexAge,
			"symbols":              indexSymbols,
			"files":                indexFiles,
			"reused_results":       indexReused,
			"promoted_low_edges":   indexLowPromoted,
			"unresolved_low_edges": indexLowUnresolved,
			"builds_count":         snap.IndexBuildCount,
			"build_avg_ms":         snap.IndexBuildAvgMs,
		},
		"tools": map[string]any{
			"registered": len(tools),
			"advertised": len(s.filteredTools()),
			"inflight":   s.Inflight(),
			"calls":      snap.ToolCallCount,
			"avg_ms":     snap.ToolCallAvgMs,
		},
		"cache": map[string]any{
			"hits":         snap.CacheHits,
			"misses":       snap.CacheMisses,
			"hit_rate_pct": fmt.Sprintf("%.1f%%", hitRatePct),
		},
		"governance": map[string]any{
			"audit_chain_length": auditLen,
			"approvals":          snap.ApprovalCount,
			"incidents":          snap.IncidentCount,
			"errors":             snap.ErrorCount,
		},
		"server": map[string]any{
			"transport": s.transport,
			"version":   serverVersion,
			"protocol":  protocolVersion,
			"roots":     s.workspaceRoots(),
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal health: %w", err)
	}
	return string(data), nil
}
