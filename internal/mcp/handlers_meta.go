// Meta tool adapter — thin root wrapper around the meta leaf package.
//
// The natural-language classifier (classifyMetaRequest and its sub-routers)
// and the semantic fallback live in internal/mcp/meta as plain functions;
// this file wires them into *Server: handleMeta builds the leaf Hooks from
// server state (dispatchTable, validPhase, costHintFor) and delegates to
// meta.Handle. The unexported shims below (classifyMetaRequest,
// validateVerifyTypes, verifyTypesExec) keep the few remaining in-package
// callers (tool_cache.go's R1 cacheability gate, handleVerify) on the same
// code paths without importing the leaf twice.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// handleMeta implements the `kern` meta-tool: it takes a natural-language
// request, classifies it via the meta leaf, dispatches to the chosen
// handler, and returns the result prefixed with the classification.
func (s *Server) handleMeta(ctx context.Context, args map[string]any) (string, error) {
	return meta.Handle(ctx, meta.Hooks{
		// RouteTool reproduces the legacy handleMeta switch exactly:
		// dispatchTable routes every name in meta.metaRoutedTools to the same
		// direct handler call the old switch made (no allowlist/cache/audit
		// wrapping — those only apply to top-level tool calls).
		RouteTool: func(ctx context.Context, name string, a map[string]any) (string, error) {
			return s.dispatchTool(ctx, "", name, a)
		},
		ValidPhase: validPhase,
		CostHint: func(tool string) (int, int) {
			h := costHintFor(tool)
			return h.EstMs, h.EstTokens
		},
		// ToolCatalog answers "give me the full tool catalog" with the same
		// metadata tools/list carries (filteredTools: name, phase, risk,
		// one-line description) instead of a symbol-search dead end (N1b).
		// ~140 tools × ~85 chars fits the 24 KiB default output budget; the
		// per-call output sandbox still caps huge catalogs at serve time.
		ToolCatalog: func() (string, error) {
			return renderToolCatalog(s.filteredTools()), nil
		},
	}, args)
}

// renderToolCatalog renders the registered tool table compactly — one line
// per tool: name, phase, risk, one-line description (same fields the
// tools/list response carries). Descriptions are flattened so the one-line
// invariant holds for the meta.Handle "(N tools)" count.
func renderToolCatalog(ts []Tool) string {
	var b strings.Builder
	for _, t := range ts {
		phase := t.Phase
		if phase == "" {
			phase = "-"
		}
		risk := t.RiskLevel
		if risk == "" {
			risk = "-"
		}
		fmt.Fprintf(&b, "%-28s %-8s %-7s %s\n", t.Name, phase, risk, strings.Join(strings.Fields(t.Description), " "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// classifyMetaRequest re-exports the leaf classifier for tool_cache.go's
// R1 cacheability gate (and its test): kern_meta responses are stored/served
// only when the sub-tool the request routes to is itself cacheable.
func classifyMetaRequest(request string) (string, map[string]any) {
	return meta.ClassifyMetaRequest(request)
}

// validateVerifyTypes re-exports the leaf verify-type gate for handleVerify
// (handlers_highlevel.go): garbage types are rejected before the exec
// firewall and before any check.
func validateVerifyTypes(types []string) error {
	return meta.ValidateVerifyTypes(types)
}

// verifyTypesExec re-exports the leaf exec-classification for handleVerify:
// a request limited to in-process types (architecture/security/dependency/
// license) must not require the exec allowlist.
func verifyTypesExec(types []string) bool {
	return meta.VerifyTypesExec(types)
}
