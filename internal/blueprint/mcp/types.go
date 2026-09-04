// Package mcp implements Blueprint's MCP tool adapter — the agent-facing
// validation surface (spec Phase 5).
//
// Blueprint exposes MCP tools that agents call to validate proposed changes
// BEFORE or AFTER writing. This is advisory pre-write validation: the agent
// opts in by calling the tool. It is NOT an OS-level file-write interception
// (spec Rule 2 / Critical safety rule, lines 970-974). Blueprint never claims
// to be a hard pre-write boundary — it is a validation API that agents use
// voluntarily, with honest documentation of its semantics.
package mcp

import (
	"context"
	"encoding/json"
)

// ToolHandler is the interface every Blueprint MCP tool implements. The MCP
// server dispatches incoming tools/call requests to the matching handler.
type ToolHandler interface {
	// Name is the MCP tool name (e.g. "blueprint_validate_staged").
	Name() string
	// Description is the human-readable tool description shown to agents.
	Description() string
	// InputSchema is the JSON Schema for the tool's arguments.
	InputSchema() map[string]interface{}
	// Handle executes the tool with the given raw JSON arguments and returns
	// a ToolResult. If isError is true, the result content describes the error.
	Handle(ctx context.Context, args json.RawMessage) ToolResult
}

// ToolResult is the MCP CallToolResult — the structured response an agent
// receives from a tool call.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is a single content block in a tool result. Blueprint always
// returns text content (JSON or human-readable).
type ToolContent struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

// NewTextResult builds a successful ToolResult with a text body.
func NewTextResult(text string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

// NewJSONResult builds a successful ToolResult with a JSON-serialized body.
func NewJSONResult(v interface{}) ToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return NewErrorResult("internal error: failed to marshal result: " + err.Error())
	}
	return NewTextResult(string(b))
}

// NewErrorResult builds an error ToolResult (isError=true).
func NewErrorResult(message string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: message}},
		IsError: true,
	}
}

// MaxPayloadBytes is the maximum accepted payload size for a single tool call
// (1 MiB). Oversized payloads are rejected before validation runs, to prevent
// abuse and protect the validation engine. (G5 scenario: "oversized payload".)
const MaxPayloadBytes = 1 << 20 // 1 MiB
