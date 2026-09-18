package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func errorResponse(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	}
}

// idKey canonicalizes a JSON-RPC id into the map key used to track in-flight
// requests, so tools/call and $/cancelRequest agree on the same key whether
// the client used a JSON number (77) or string ("77") id. A raw id that does
// not parse is used verbatim.
func idKey(id json.RawMessage) string {
	var v any
	if err := json.Unmarshal(id, &v); err != nil || v == nil {
		return string(id)
	}
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}
func (s *Server) toolCallResponse(id json.RawMessage, params json.RawMessage) any {
	// First real use: start background indexing (no-op if initialize already
	// triggered it — indexOnce fires exactly once per server lifetime).
	s.indexOnce.Do(func() { go s.preloadIndexes() })
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	key := idKey(id)
	// Pre-tool-use hook: deny the call before any side effect runs. The hook
	// is optional (nil = no-op) and returns nil to allow, or an error to
	// reject — rejection is reported as a tool error (isError=true) so the
	// agent sees why the call was blocked.
	if s.preTool != nil {
		if err := s.preTool(p.Name, p.Arguments); err != nil {
			denied := fmt.Sprintf("pre-tool-use denied: %s", err)
			result := map[string]any{
				"content": []any{map[string]any{"type": "text", "text": denied}},
				"isError": true,
			}
			attachTokenMetadata(result, p.Name, p.Arguments, denied)
			return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		}
	}
	// A generous 30-minute per-call ceiling so a hung subprocess (an
	// unresponsive Ollama during plan/analyze, or a slow index build) can
	// never wedge the server goroutine forever; long legitimate operations
	// (kern_execute sandbox builds, kern_verify full suites) run within it.
	// This is the effective cap for plugin-driven calls: it must be >= the
	// plugin MAX_CEILING_MS (kern.ts) so the agent's requested timeout
	// governs. The exec-family handlers install their own shorter deadlines
	// on top.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// The per-call scope carries the index loaded during this tool's execution
	// so provenance is stamped from this call's index, never another's. It is
	// scoped here instead of on the Server struct to avoid cross-talk between
	// concurrent tool calls.
	scope := &indexScope{}
	ctx = context.WithValue(ctx, indexScopeKey{}, scope)
	s.registerInflight(key, cancel)
	defer func() {
		cancel()
		s.unregisterInflight(key)
	}()
	text, err := func() (out string, runErr error) {
		defer func() {
			if rec := recover(); rec != nil {
				runErr = fmt.Errorf("panic in tool %s: %v", p.Name, rec)
				fmt.Fprintf(os.Stderr, "kern-mcp: panic running %s: %v\n%s\n", p.Name, rec, debug.Stack())
			}
		}()
		return s.runTool(ctx, key, p.Name, p.Arguments)
	}()
	// Cap every tool response at the output budget so a large result cannot
	// flood the agent's context. Overridable per call with max_output=N.
	if err == nil {
		var budget int
		budget, err = callOutputBudget(p.Arguments)
		if err == nil {
			text = sandboxOutput(text, budget, p.Name)
		}
	}
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": false,
	}
	// Structured provenance (P1.2): retrieval handlers stamp it on the
	// per-call scope; any other tool that loaded an index gets
	// index-identity-only raw provenance. The one-line summary appended to
	// the content text is derived from the same structured field, so there
	// is a single source of truth for index evidence.
	if scope.prov == nil && scope.ix != nil {
		scope.prov = s.rawProvenance(scope.ix, nil)
	}
	if err != nil {
		switch {
		case text != "":
			// The handler returned a payload alongside its error — e.g. the
			// auditable denial proof from kern_authorize_context or a
			// blueprint gate BLOCK result. Deliver both, error first, so the
			// denial itself stays auditable (the handler's documented
			// contract) instead of being replaced by the bare error text.
			text = err.Error() + "\n" + text
		case scope.prov != nil:
			// Errors can carry provenance too: governed denials attach the
			// auditable authorizing rule alongside the error text.
			text = err.Error() + "\n" + s.provenanceSummary(scope.ix, scope.prov)
			result["provenance"] = scope.prov
		default:
			text = err.Error()
		}
		result["content"] = []any{map[string]any{"type": "text", "text": text}}
		result["isError"] = true
	} else if scope.prov != nil {
		result["provenance"] = scope.prov
		result["content"] = []any{map[string]any{"type": "text", "text": text + "\n" + s.provenanceSummary(scope.ix, scope.prov)}}
	}
	// Structured token metadata: the request tokens were counted
	// before processing; count the final response text (provenance summary
	// included) after and stamp the ledger on the result.
	out := text
	if content, ok := result["content"].([]any); ok && len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			if t, ok := first["text"].(string); ok {
				out = t
			}
		}
	}
	attachTokenMetadata(result, p.Name, p.Arguments, out)
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

// defaultOutputBudget is the MCP output sandbox cap in bytes, used when the
// agent does not pass max_output= and KERN_MCP_MAX_OUTPUT is unset. ~6K tokens
// of safety net for a single tool result.
const defaultOutputBudget = 24 << 10

// outputBudget resolves the global cap from KERN_MCP_MAX_OUTPUT (bytes).
func outputBudget() int {
	if v := os.Getenv("KERN_MCP_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultOutputBudget
}

// callOutputBudget returns the per-call budget: an explicit max_output=N
// argument (bytes; 0 disables the sandbox) wins over the global cap. A
// malformed max_output is an error, not a silent fallback.
func callOutputBudget(args map[string]any) (int, error) {
	if v := argString(args, "max_output"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("max_output: invalid integer %q", v)
		}
		if n <= 0 {
			return 0, nil // disabled for this call
		}
		return n, nil
	}
	return outputBudget(), nil
}

// sandboxOutput truncates text to budget bytes (when budget > 0) and stamps a
// marker with before/after token counts and a tool-specific recovery hint. The
// marker doubles as the anti-context-flood boundary: an agent that needs more
// can re-call with a larger max_output or a narrower tool.
func sandboxOutput(text string, budget int, tool string) string {
	if budget <= 0 || len(text) <= budget {
		return text
	}
	// Trim to a rune-safe boundary: slicing mid-multi-byte-rune would leave a
	// dangling UTF-8 sequence that corrupts the marker's own token counts and
	// any downstream tokenizer.
	cut := budget
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n\n… [MCP output sandbox: %d → %d chars (%d → %d tokens). %s Pass max_output=N to this tool for more, or narrow the request.]",
		len(text), cut, tokenize.Count(text), tokenize.Count(text[:cut]), recoveryHint(tool))
}

// recoveryHint suggests the narrower tool to recover the truncated detail.
func recoveryHint(tool string) string {
	switch tool {
	case "kern_project_map", "kern_compact_file":
		return "Use kern_context or kern_compact_file for specific symbols instead."
	case "kern_walk":
		return "Use a shallower depth= or a different root symbol."
	case "kern_near":
		return "Lower max= or depth=."
	case "kern_context":
		return "Request fewer lines=."
	case "kern_review", "kern_context_budget":
		return "Lower max_tokens=."
	case "kern_doc_search":
		return "Narrow the query or lower k=."
	case "kern_ast_search":
		return "Tighten the pattern."
	case "kern_exec":
		return "Cap the script's own output with max=."
	case "kern_graph", "kern_arch", "kern_hubs":
		return "This report is inherently large; prefer kern_search/kern_context for specifics."
	default:
		return "Narrow the query."
	}
}

func (s *Server) promptGetResponse(id json.RawMessage, params json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	var def *Prompt
	for i := range prompts {
		if prompts[i].Name == p.Name {
			def = &prompts[i]
			break
		}
	}
	if def == nil {
		return errorResponse(id, -32602, "prompt not found: "+p.Name)
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{
			"description": def.Description,
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": promptText(p.Name, p.Arguments),
					},
				},
			},
		},
	}
}

// argString reads an optional scalar string tool argument. Missing or null
// returns "". Accepted input shapes follow the documented coercion contract
// (D6): strings verbatim (trimmed), JSON numbers to their canonical string
// form ("12345" for 12345) and booleans for the boolean-style string flags
// (semantic/force/apply read via argBool). null/object/array values for
// string-typed arguments are rejected before dispatch by validateStringArgs,
// so a handler never sees "map[]" or "[]" materialize here.
func argString(args map[string]any, key string) string {
	return mcpargs.ArgString(args, key)
}

// argStrings reads an optional array-of-strings tool argument. Accepts a
// []any / []string (MCP JSON arrays) and, for lenient clients that pass
// everything as a single string, a comma- or whitespace-separated list.
// Values are trimmed and empty entries dropped; missing/nil returns nil.
func argStrings(args map[string]any, key string) []string {
	return mcpargs.ArgStrings(args, key)
}

// argBool reads an optional boolean tool argument. Accepts native bools and
// the strings "true"/"1" (MCP clients often pass everything as strings).
func argBool(args map[string]any, key string) bool {
	return mcpargs.ArgBool(args, key)
}

// atoiArg parses an integer tool argument, falling back to def for empty
// input. A malformed value is an error, not a silent default, so a typo'd
// number can't quietly zero out a limit or mis-size a buffer.
func atoiArg(v string, def int) (int, error) {
	return mcpargs.AtoiArg(v, def)
}

// validateStringArgs enforces the documented MCP argument-coercion contract
// (D6) at the dispatch choke point (runTool). For every property the tool's
// inputSchema declares with type "string", a present argument must be a JSON
// string, a JSON number (coerced to its canonical string form by argString —
// plugin/JSON callers legitimately pass numeric strings) or a JSON boolean
// (the strProp boolean flags like semantic/force/apply round-trip through
// argBool/argString). null, object and array values are rejected with a clear
// error naming the argument, the tool and the expected type, so query:{} or
// query:[...] surface as an isError instead of a silent "no symbols matched".
// Arguments not declared in the schema (max_output, agent_id, ...) and
// non-string-typed properties (integer, boolean, object, array) pass through
// untouched — those already fail loud in their handlers (atoiArg's "invalid
// integer", for example). Numeric-typed arguments keep their lenient parsing
// and any existing bounds enforcement unchanged. Unknown tool names validate
// nothing.
func validateStringArgs(name string, args map[string]any) error {
	for i := range tools {
		if tools[i].Name != name {
			continue
		}
		props, _ := tools[i].InputSchema["properties"].(map[string]any)
		for key, raw := range props {
			prop, _ := raw.(map[string]any)
			if prop["type"] != "string" {
				continue
			}
			v, ok := args[key]
			if !ok {
				continue // absent optional argument: nothing to coerce
			}
			switch v.(type) {
			case string, float64, int, bool:
				// Accepted scalar coercions (int covers Go-API callers; JSON
				// numbers always decode to float64 in this server).
			case nil:
				return fmt.Errorf("argument %q for tool %s: expected a string, got null", key, name)
			case []string, []any:
				return fmt.Errorf("argument %q for tool %s: expected a string, got an array", key, name)
			case map[string]any:
				return fmt.Errorf("argument %q for tool %s: expected a string, got an object", key, name)
			default:
				return fmt.Errorf("argument %q for tool %s: expected a string, got %T", key, name, v)
			}
		}
		return nil
	}
	return nil
}
