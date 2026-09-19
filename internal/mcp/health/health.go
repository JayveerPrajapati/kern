// Package health owns the server health & diagnostics MCP tool body (kern_health)
// as plain functions.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// ServerInfo provides server-level metadata and hooks to the health reporter.
type ServerInfo struct {
	Transport       string
	Version         string
	Protocol        string
	Roots           []string
	ToolsRegistered int
	ToolsAdvertised int
	Inflight        int
	AuditLength     int
	CachedIndex     func(root string) (*index.Index, bool)
}

// Health returns a structured real-time health snapshot of the kern MCP server.
func Health(ctx context.Context, info ServerInfo, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" && len(info.Roots) > 0 {
		root = info.Roots[0]
	}

	snap := metrics.Default().Snapshot()

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
		if info.CachedIndex != nil {
			if ix, ok := info.CachedIndex(root); ok && ix != nil {
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

	hitRatePct := 0.0
	totalCache := snap.CacheHits + snap.CacheMisses
	if totalCache > 0 {
		hitRatePct = (float64(snap.CacheHits) / float64(totalCache)) * 100
	}

	result := map[string]any{
		"status":  "ok",
		"version": info.Version,
		"index":   indexBlock,
		"tools": map[string]any{
			"registered": info.ToolsRegistered,
			"advertised": info.ToolsAdvertised,
			"inflight":   info.Inflight,
			"calls":      snap.ToolCallCount,
			"avg_ms":     snap.ToolCallAvgMs,
		},
		"cache": map[string]any{
			"hits":         snap.CacheHits,
			"misses":       snap.CacheMisses,
			"hit_rate_pct": fmt.Sprintf("%.1f%%", hitRatePct),
		},
		"governance": map[string]any{
			"audit_chain_length": info.AuditLength,
			"approvals":          snap.ApprovalCount,
			"incidents":          snap.IncidentCount,
			"errors":             snap.ErrorCount,
		},
		"server": map[string]any{
			"transport": info.Transport,
			"version":   info.Version,
			"protocol":  info.Protocol,
			"roots":     info.Roots,
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal health: %w", err)
	}
	return string(data), nil
}
