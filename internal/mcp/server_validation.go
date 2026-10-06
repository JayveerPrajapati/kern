package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcpserve"
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
		Meta      struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	// MCP spec: progress notifications are only emitted when the client
	// supplied a progressToken in the request's _meta — never unsolicited.
	// Stringify the token the same way ids are canonicalized (string kept
	// verbatim, number to its canonical form); absent/null yields "", which
	// suppresses progress entirely.
	token := ""
	if p.Meta.ProgressToken != nil && string(p.Meta.ProgressToken) != "null" {
		token = idKey(p.Meta.ProgressToken)
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
			mcpserve.AttachTokenMetadata(result, p.Name, p.Arguments, denied)
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
	scope := &indexScope{token: token}
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
		return s.runTool(ctx, key, token, p.Name, p.Arguments)
	}()
	// Cap every tool response at the output budget so a large result cannot
	// flood the agent's context. Overridable per call with max_output=N and
	// per tool by the R7 per-tool table (mcpserve.CallOutputBudget). A
	// truncated response retains its FULL pre-sandbox text under an anchor
	// (retainOutput) and the marker advertises slice=<anchor>:lines:A-B|tail:N
	// to read the elided part without re-running the tool (R7 "more" cursor,
	// see retain.go). A slice re-read (scope.sliced) is bounded by the SAME
	// budget (A3): an over-budget slice is truncated and mints a FRESH anchor
	// for the ELIDED REMAINDER of the slice itself, so a wide
	// slice=lines:1-999999 can never pull the whole retained output into
	// context uncapped — the recovery path chains (each truncated slice
	// points at the next) instead of flooding, and never re-executes the tool.
	if err == nil {
		var budget int
		budget, err = mcpserve.CallOutputBudget(p.Name, p.Arguments)
		if err == nil && budget > 0 && len(text) > budget {
			if scope.sliced {
				// Chained cursor: retain only the elided remainder (text[cut:])
				// of this slice under a fresh anchor, so the marker's slice=
				// advice reads the part the caller has NOT seen yet — never the
				// whole retained entry.
				cut := mcpserve.OutputCut(text, budget)
				anchor := retainOutput(text[cut:])
				text = mcpserve.SandboxOutputRetained(text, budget, p.Name, anchor)
			} else {
				anchor := retainOutput(text)
				text = mcpserve.SandboxOutputRetained(text, budget, p.Name, anchor)
			}
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
	// Track whether the handler explicitly attached provenance (a governed
	// denial) versus the raw auto-fill below: errors only carry the index
	// stamp when it is genuinely load-bearing. A bare argument error like
	// "query is required" must not drag the index banner along (QA F6).
	explicitProv := scope.prov != nil
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
		case explicitProv:
			// Errors can carry provenance too: governed denials attach the
			// auditable authorizing rule alongside the error text.
			text = err.Error() + "\n" + s.provenanceSummary(scope.ix, scope.prov)
			result["provenance"] = scope.prov
		default:
			text = err.Error()
		}
		result["content"] = []any{map[string]any{"type": "text", "text": text}}
		result["isError"] = true
	} else if scope.prov != nil && (!scope.unchanged || !etag.Eligible(p.Name)) {
		// An unchanged short-circuit (ADR-0012) is deliberately tiny: the
		// provenance summary line is skipped so the response stays exactly
		// "unchanged (etag E)" plus the etag/unchanged result fields.
		// F-3: the unchanged flag belongs to the last INNER composed step
		// (steps re-enter runTool under this same scope), never to a
		// non-eligible outer tool like kern_compose — a short-circuited last
		// step must not strip the outer envelope's provenance.
		result["provenance"] = scope.prov
		result["content"] = []any{map[string]any{"type": "text", "text": text + "\n" + s.provenanceSummary(scope.ix, scope.prov)}}
	}
	// ETag conditional-fetch (ADR-0012, B1): every eligible response —
	// no-etag calls, etag-mismatch calls and the unchanged short-circuit
	// alike — carries its response etag; the short-circuit additionally sets
	// unchanged=true. The working-set registry records the (tool, args
	// digest, etag, time) the caller was handed so kern_meta "my working
	// set" can list it; the raw agent_id arg keys the bucket ("" -> "_").
	// The Eligible gate keeps a NON-eligible tool envelope (kern_compose)
	// from inheriting the last inner step's scope.etag: composed steps
	// re-enter runTool under this same scope, so without the gate the outer
	// envelope would wrongly attach an inner step's etag/unchanged fields
	// and record (kern_compose, E1) in the working set (HIGH-1).
	if scope.etag != "" && etag.Eligible(p.Name) {
		result["etag"] = scope.etag
		if scope.unchanged {
			result["unchanged"] = true
		}
		etag.Default.Record(argString(p.Arguments, "agent_id"), etag.Entry{
			Tool:  p.Name,
			Args:  etag.CanonicalArgsKey(p.Arguments),
			ETag:  scope.etag,
			Stamp: time.Now(),
		})
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
	// Per-tool token ledger: one entry per executed tool call (never for
	// pre-tool denials — they return before this point). The same final
	// response text the client sees is what gets counted, so the ledger
	// answers "which tools return the most tokens to my context".
	mcpserve.RecordToolCall(p.Name, p.Arguments, out, s.clientNameFor())
	mcpserve.AttachTokenMetadata(result, p.Name, p.Arguments, out)
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

// clientNameFor returns the client identity captured at the initialize
// handshake (clientInfo.name), "" when no client has initialized yet.
// Mutex-guarded like the schemaVersion state it is stored alongside.
func (s *Server) clientNameFor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientName
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

// progressToken returns the client's MCP progress token for the current tool
// call, or "" when the client did not opt in (no _meta.progressToken). Slow
// handlers (kern_verify) use it to emit phase-level progress notifications;
// the empty-token case is a no-op in s.progress (M4).
func progressToken(ctx context.Context) string {
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		return scope.token
	}
	return ""
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
				// numbers always decode to float64 in this server). The D6
				// contract coerce numbers to canonical string form for genuine
				// string args (plugin callers legitimately pass numeric strings)
				// — but a JSON NUMBER for the path-typed "root" argument is a
				// client bug, not a path: coercing it to "12345" would resolve
				// silently relative to the workspace root.
				if key == "root" {
					switch v.(type) {
					case float64, int:
						return fmt.Errorf("argument %q for tool %s: expected a path string, got a number", key, name)
					}
				}
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
