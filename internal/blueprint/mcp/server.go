// Package mcp implements Blueprint's minimal MCP (Model Context Protocol)
// server: a newline-delimited JSON-RPC 2.0 server over stdio that exposes
// Blueprint's validation tools to MCP clients (agents).
//
// Implemented methods: initialize, notifications/initialized, tools/list,
// tools/call, and shutdown. Unknown methods return JSON-RPC error -32601.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// protocolVersion is the MCP protocol version this server speaks.
const protocolVersion = "2024-11-05"

// JSON-RPC 2.0 error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

// rpcRequest is a JSON-RPC 2.0 request or notification. ID is nil for
// notifications (messages the client does not expect a response to).
type rpcRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response. Exactly one of Result or Error is set.
type rpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams is the params object of a tools/call request.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// errLineTooLong signals that a single input line exceeded MaxPayloadBytes.
// The remainder of the line is drained so the stream stays in sync, and the
// line is skipped.
var errLineTooLong = errors.New("mcp: input line exceeds maximum payload size")

// Server is a minimal MCP server that serves Blueprint's tools over stdio
// using newline-delimited JSON-RPC 2.0.
type Server struct {
	name    string
	version string
	tools   map[string]ToolHandler
	// preTool is an optional pre-tool-use gate (mirrors kern's MCP
	// Server.WithPreToolHook). It runs before every tool handler; a nil hook
	// is a no-op, and a non-nil error denies the call before any side effect.
	preTool func(name string, args map[string]any) error
}

// NewServer returns an MCP server with the given name and version and no tools
// registered.
func NewServer(name, version string) *Server {
	return &Server{
		name:    name,
		version: version,
		tools:   make(map[string]ToolHandler),
	}
}

// WithPreToolHook registers a pre-tool-use gate with the same signature and
// semantics as kern's MCP Server.WithPreToolHook: purely additive, a nil hook
// (the default) leaves every tools/call untouched, and a returned error denies
// the call — surfaced as a tool error (isError=true) before the handler runs.
// This is the seam where Blueprint's control plane gates agent tool calls
// (spec Phase 5, "PreToolUse behavior").
func (s *Server) WithPreToolHook(fn func(name string, args map[string]any) error) *Server {
	s.preTool = fn
	return s
}

// RegisterTool registers an MCP tool handler with the server. Registering a
// second tool with the same name replaces the first.
func (s *Server) RegisterTool(h ToolHandler) {
	s.tools[h.Name()] = h
}

// Serve reads newline-delimited JSON-RPC 2.0 messages from r and writes one
// response line per request to w until the stream is exhausted (client closed
// stdin) or ctx is cancelled.
//
// Graceful handling: notifications receive no response; malformed JSON lines,
// lines without a method, empty lines, and lines exceeding MaxPayloadBytes are
// skipped without crashing; unknown methods return error -32601; panics inside
// a tool handler are recovered and returned as error -32603.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	enc := json.NewEncoder(writer)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := readLine(reader)
		if err == io.EOF {
			return nil
		}
		if err == errLineTooLong {
			continue // line already drained and skipped
		}
		if err != nil {
			return err
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
			continue // malformed JSON or missing method: skip, never crash
		}

		notification := len(req.ID) == 0 || bytes.Equal(bytes.TrimSpace(req.ID), []byte("null"))
		result, rpcErr := s.dispatch(ctx, req)

		if notification {
			continue // notifications get no response
		}

		resp := rpcResponse{Jsonrpc: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if err := enc.Encode(resp); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

// dispatch routes a decoded request to its method handler.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) (interface{}, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.initialize(), nil
	case "notifications/initialized":
		// Notification: nothing to do. If sent with an ID, respond with an
		// empty result.
		return struct{}{}, nil
	case "tools/list":
		return s.listTools(), nil
	case "tools/call":
		return s.callTool(ctx, req)
	case "shutdown":
		return struct{}{}, nil
	default:
		return nil, &rpcError{Code: rpcMethodNotFound, Message: "method not found"}
	}
}

// initialize returns the initialize handshake result.
func (s *Server) initialize() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    s.name,
			"version": s.version,
		},
	}
}

// listTools returns the registered tools (name, description, inputSchema) in
// deterministic name order.
func (s *Server) listTools() map[string]interface{} {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	tools := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		h := s.tools[name]
		tools = append(tools, map[string]interface{}{
			"name":        h.Name(),
			"description": h.Description(),
			"inputSchema": h.InputSchema(),
		})
	}
	return map[string]interface{}{"tools": tools}
}

// callTool dispatches a tools/call request to the matching handler and wraps
// its ToolResult. Panics inside the handler are recovered and reported as
// JSON-RPC error -32603.
func (s *Server) callTool(ctx context.Context, req rpcRequest) (result interface{}, rpcErr *rpcError) {
	var params toolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: rpcInvalidParams, Message: "invalid params: " + err.Error()}
		}
	}
	if params.Name == "" {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "missing tool name"}
	}

	h, ok := s.tools[params.Name]
	if !ok {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "unknown tool: " + params.Name}
	}
	if len(params.Arguments) > MaxPayloadBytes {
		return nil, &rpcError{
			Code:    rpcInvalidParams,
			Message: fmt.Sprintf("tool arguments exceed %d bytes", MaxPayloadBytes),
		}
	}

	// Pre-tool-use gate: deny the call before any side effect runs. A nil hook
	// is a no-op; a returned error is surfaced as a tool error (isError=true)
	// so the agent sees why the call was blocked (spec Phase 5, PreToolUse).
	if s.preTool != nil {
		var args map[string]any
		if len(params.Arguments) > 0 && string(params.Arguments) != "null" {
			_ = json.Unmarshal(params.Arguments, &args) // best-effort: hook gets what it can
		}
		if err := s.preTool(params.Name, args); err != nil {
			return NewErrorResult("pre-tool-use denied: " + err.Error()), nil
		}
	}

	defer func() {
		if rec := recover(); rec != nil {
			result = nil
			rpcErr = &rpcError{Code: rpcInternalError, Message: fmt.Sprintf("tool %q panicked: %v", params.Name, rec)}
		}
	}()

	return h.Handle(ctx, params.Arguments), nil
}

// readLine reads one newline-delimited line. A trailing newline is included in
// the returned slice; the final line without a newline is returned on EOF. If
// a line exceeds MaxPayloadBytes the remainder is drained (stream stays in
// sync) and errLineTooLong is returned.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line, nil
		case err == bufio.ErrBufferFull:
			if len(line) > MaxPayloadBytes {
				// Oversized line: drain the rest so subsequent lines stay in sync.
				for err == bufio.ErrBufferFull {
					_, err = r.ReadSlice('\n')
				}
				if err == nil {
					return nil, errLineTooLong
				}
				return nil, err
			}
			continue
		case err == io.EOF && len(line) > 0:
			return line, nil
		default:
			return nil, err
		}
	}
}
