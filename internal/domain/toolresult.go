package domain

// ToolCallResult is the structured outcome of a governed tool call (the
// /v1/tools/{name} passthrough and the SDK catalog). It carries the raw tool
// output plus the conditional-fetch fields when the tool is eligible
// (ADR-0012): Etag identifies the exact response view — pass it back as the
// "etag" argument to conditionally collapse a re-read — and Unchanged is
// true when the caller's etag matched and the full text was elided; Output
// is then the self-describing "unchanged (etag ...)" one-liner, so callers
// that cannot see the structured fields still learn how to recover.
// internal/mcp and internal/web share this type because web cannot import
// mcp (the mcp → org → enterprise → web import cycle).
type ToolCallResult struct {
	Output    string
	Etag      string
	Unchanged bool
}
