package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
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

	// Index health & freshness: the in-memory session cache when the server
	// has loaded an index for this root; otherwise fall back to the persisted
	// disk index (index.DiskIndexView) so a long-lived server with a cold
	// session cache never reports a healthy on-disk index as missing — the
	// same disk-authoritative block `kern health` shows.
	cacheLoaded := false
	indexBlock := map[string]any{
		"root":                 root,
		"fresh":                false,
		"age":                  "unknown",
		"symbols":              0,
		"files":                0,
		"reused_results":       0,
		"promoted_low_edges":   0,
		"unresolved_low_edges": 0,
		"builds_count":         snap.IndexBuildCount,
		"build_avg_ms":         snap.IndexBuildAvgMs,
	}
	if root != "" {
		sess := s.sessionFor(root)
		if sess != nil {
			if ix, ok := sess.CachedIndex(); ok && ix != nil {
				cacheLoaded = true
				indexBlock["fresh"] = true
				if !ix.UpdatedAt.IsZero() {
					indexBlock["age"] = time.Since(ix.UpdatedAt).Round(time.Second).String()
				}
				indexBlock["symbols"] = len(ix.Symbols)
				indexBlock["files"] = len(ix.FileHashes)
				indexBlock["reused_results"] = ix.ReusedResults()
				indexBlock["promoted_low_edges"] = ix.PromotedLowEdges
				indexBlock["unresolved_low_edges"] = ix.UnresolvedLowEdges
			}
		}
		if !cacheLoaded {
			if disk := index.DiskIndexView(root); disk != nil {
				disk["note"] = "in-memory session cache empty; showing persisted disk index"
				disk["builds_count"] = snap.IndexBuildCount
				disk["build_avg_ms"] = snap.IndexBuildAvgMs
				indexBlock = disk
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
		"index":   indexBlock,
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
