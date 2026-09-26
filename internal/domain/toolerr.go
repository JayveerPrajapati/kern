package domain

import "errors"

// Tool dispatch sentinel errors (persona audit iteration 7, finding 6).
//
// The governed MCP dispatch path (internal/mcp: the confinement gate, the
// KERN_TOOLS allowlist, root validation, the safety budget and the RBAC
// funnel) classifies every pre-execution denial as ErrToolDenied and every
// unknown tool name as ErrToolUnknown. They live here — not in internal/mcp
// as originally planned — because the REST passthrough (internal/web) maps
// HTTP status from them with errors.Is, and internal/web cannot import
// internal/mcp (the mcp → mcp/org → enterprise → web import cycle). domain
// is the shared kernel both packages already import, so the sentinels stay
// typed end to end without the cycle.
var (
	// ErrToolDenied marks a tool call refused before any handler side
	// effect ran (confinement gate, allowlist, root validation, safety
	// budget, RBAC). REST/sdk callers map it to 403.
	ErrToolDenied = errors.New("tool call denied")
	// ErrToolUnknown marks a tool name with no registered handler. REST/sdk
	// callers map it to 404.
	ErrToolUnknown = errors.New("unknown tool")
)
