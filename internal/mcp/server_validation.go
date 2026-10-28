package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/mcpserve"
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

// toolCallParams is the decoded {name, arguments, _meta} body of a
// tools/call request. dispatch decodes it ONCE per call and shares the
// struct with the confinement-gate check and toolCallResponseDecoded, so the
// params are JSON-decoded exactly once (the gate previously re-unmarshalled
// what toolCallResponse had already decoded).
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Meta      struct {
		ProgressToken json.RawMessage `json:"progressToken"`
	} `json:"_meta"`
}

// toolCallResponse decodes the tools/call params and delegates to
// toolCallResponseDecoded. The production path (dispatch) decodes once for
// the confinement gate and calls toolCallResponseDecoded directly; this
// wrapper keeps direct callers (tests) on the same code path.
func (s *Server) toolCallResponse(id json.RawMessage, params json.RawMessage) any {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	return s.toolCallResponseDecoded(id, p)
}

func (s *Server) toolCallResponseDecoded(id json.RawMessage, p toolCallParams) any {
	// First real use: start background indexing (no-op if initialize already
	// triggered it — indexOnce fires exactly once per server lifetime).
	s.indexOnce.Do(func() { go s.preloadIndexes() })
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
			mcpserve.AttachTokenMetadata(result, p.Name, p.Arguments, tokenize.Count(denied))
			return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		}
	}
	// F1 (MCP spec parity): tools/call with an unknown tool name is an
	// "invalid params" JSON-RPC error (-32602) — matching the spec's
	// tools/call example and this server's prompts/get handling — instead of
	// an isError tool result. Handler-level EXECUTION errors (including a
	// handler's own "X is required" checks) still surface as isError tool
	// results; pre-dispatch governance denials (confinement/allowlist) keep
	// the isError framing above so the agent sees why the call was blocked.
	// String-argument shape violations (D6) are enforced ONCE at the runTool
	// choke point (validateArgTypes) so the two pre-dispatch call sites
	// cannot disagree — and they surface as isError tool results naming the
	// argument, the tool and the expected type, never as the -32602 protocol
	// error, which stays reserved for F1 unknown/empty tool names above
	// (pinned by TestStringArgViolationIsToolErrorNotJSONRPC).
	if p.Name == "" {
		return errorResponse(id, -32602, "invalid arguments: missing tool name")
	}
	if !knownTool(p.Name) {
		return errorResponse(id, -32602, "unknown tool: "+p.Name)
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
		// The structured provenance must respect the same per-call output
		// budget that bounds the content text: the whole envelope — not
		// just the text — is the agent's context cost, and the count-only
		// maxResponseSymbols cap let a ~19KB symbols listing ship under a
		// 500-byte budget. The fixed attestation header always stays; only
		// the Symbols listing shrinks (capProvenanceSymbolsToFit).
		if err == nil && budget > 0 {
			capProvenanceSymbolsToFit(scope.prov, budget)
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
		ident := ""
		if scope.ix != nil && scope.ix.Identity != nil {
			ident = indexIdentityString(scope.ix.Identity)
		}
		etag.Default.Record(argString(p.Arguments, "agent_id"), etag.Entry{
			Tool:     p.Name,
			Args:     etag.CanonicalArgsKey(p.Arguments),
			ETag:     scope.etag,
			Stamp:    time.Now(),
			Identity: ident,
			View:     serveView(p.Name, p.Arguments),
		})
	}
	// Structured token metadata: the request tokens were counted
	// before processing; count the final response text (provenance summary
	// included) exactly ONCE after — tokenize.Count over the exact text the
	// client sees — and share the count with both the per-tool ledger
	// (RecordToolCall) and the response metadata (AttachTokenMetadata) so the
	// full-text count never runs twice per call. SandboxOutput's own
	// before/after counts (over text and text[:cut]) are separate substrings
	// and stay inside the sandbox marker.
	out := text
	if content, ok := result["content"].([]any); ok && len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			if t, ok := first["text"].(string); ok {
				out = t
			}
		}
	}
	// W10/W21 hints, appended to the content text AFTER the handler ran:
	// W10 names arguments the tool's schema does not declare (a 'roots'-vs-
	// 'root' typo used to run against the default root with isError:false —
	// the caller never learned the argument was dropped); W21 bounds large
	// responses from max_tokens-capable tools.
	extra := ""
	if result["isError"] != true {
		if warn := unknownParamsHint(p.Name, p.Arguments); warn != "" {
			extra = warn
		}
	}
	if hint, ok := largeOutputHint(p.Name, p.Arguments, out, result); ok {
		if extra != "" {
			extra += "\n"
		}
		extra += hint
	}
	if extra != "" {
		out += "\n" + extra
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if first, ok := content[0].(map[string]any); ok {
				if _, isText := first["text"].(string); isText {
					first["text"] = out
				}
			}
		}
	}
	tokensOut := tokenize.Count(out)
	// Per-tool token ledger: one entry per executed tool call (never for
	// pre-tool denials — they return before this point). The same final
	// response text the client sees is what gets counted, so the ledger
	// answers "which tools return the most tokens to my context".
	mcpserve.RecordToolCall(p.Name, p.Arguments, tokensOut, s.clientNameFor())
	mcpserve.AttachTokenMetadata(result, p.Name, p.Arguments, tokensOut)
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

// largeOutputHintThreshold is the returned-token count above which an
// unbounded default view from a max_tokens-capable tool earns the bounding
// hint (W21, 2026-10-26 black-box audit): ~4000 tokens is where a single
// call starts costing a meaningful slice of an agent's context window.
const largeOutputHintThreshold = 4000

// largeOutputHint decides whether a large tool response should carry the
// bounding hint. It fires only when the tool's schema declares a max_tokens
// parameter, the caller did not supply one, the response is a successful
// (non-error, non-unchanged) text payload, and the payload exceeds the
// threshold. The hint names the argument so the very next call can be
// cheaper — without it the cost shows up in the response metadata but the
// fix stays invisible (W21 finding: kern_explore's default on a 2GB repo
// returned ~8.7k tokens with no in-band guidance).
func largeOutputHint(name string, args map[string]any, out string, result map[string]any) (string, bool) {
	if result["isError"] == true || result["unchanged"] == true {
		return "", false
	}
	if tokenize.Count(out) < largeOutputHintThreshold {
		return "", false
	}
	t, ok := toolByName[name]
	if !ok {
		return "", false
	}
	props, _ := t.InputSchema["properties"].(map[string]any)
	if _, ok := props["max_tokens"]; !ok {
		return "", false
	}
	if v, ok := args["max_tokens"]; ok && v != nil {
		if s, isStr := v.(string); !isStr || strings.TrimSpace(s) != "" {
			return "", false // the caller already bounded (or chose) the size
		}
	}
	return fmt.Sprintf("[kern hint: this response is ~%d tokens; pass max_tokens (e.g. max_tokens: 2000) to bound future %s responses]", tokenize.Count(out), name), true
}

// governancePassthroughParams are the governance-injection argument keys a
// governed client may legitimately attach to ANY tool call (kern_context
// governed mode): they are never flagged as unknown by unknownParamsHint.
var governancePassthroughParams = map[string]bool{
	"agent_id": true, "agent_token": true, "task": true, "scope": true,
}

// unknownParamsHint names arguments the tool's schema does not declare
// (W10, 2026-10-26 black-box audit): a typo like 'roots' for 'root' used to
// run against the default root with isError:false — the argument was silently
// dropped and the caller never learned. The hint lists the ignored keys and
// the declared parameter names so the fix is visible on the very next call.
// It fires only for non-error results (an error response already has the
// caller's attention) and never for governance passthrough keys.
func unknownParamsHint(name string, args map[string]any) string {
	t, ok := toolByName[name]
	if !ok || len(args) == 0 {
		return ""
	}
	props, _ := t.InputSchema["properties"].(map[string]any)
	var unknown []string
	for k := range args {
		if _, declared := props[k]; !declared && !governancePassthroughParams[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	declared := make([]string, 0, len(props))
	for k := range props {
		declared = append(declared, k)
	}
	sort.Strings(declared)
	return fmt.Sprintf("[kern warning: %s has no parameter(s) %v — ignored; declared parameters: %v]", name, unknown, declared)
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

// knownTool reports whether name has a registered handler. The dispatch
// table is the execution authority — runTool's dispatchTool resolves through
// it, and test-only handlers register there without catalog rows — so table
// membership (not catalog advertisement membership) is the correct boundary
// test for the -32602 unknown-tool rejection (F1).
func knownTool(name string) bool {
	_, ok := dispatchTable[name]
	return ok
}

// argString reads an optional scalar string tool argument. Missing or null
// returns "". Accepted input shapes follow the documented coercion contract
// (D6): strings verbatim (trimmed), JSON numbers to their canonical string
// form ("12345" for 12345) and booleans for the boolean-style string flags
// (semantic/force/apply read via argBool). null/object/array values for
// string-typed arguments are rejected before dispatch by validateArgTypes,
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

// validateNonStringScalar enforces the D6 fail-loud contract for
// schema-declared integer/number/boolean properties. Lenient accepted forms
// (exactly the forms ArgBool/AtoiArg/ArgString actually honor): JSON numbers
// and numeric strings for integer/number; JSON booleans, the strings
// "true"/"false"/"1"/"0" and the numbers 0/1 for boolean. Everything else —
// null, arrays, objects, non-numeric strings, floats where an integer is
// declared — returns a clean error naming the argument, the tool and the
// expected type, instead of a silent default (limit:{} -> 50) or a silent
// false (semantic:"yes").
func validateNonStringScalar(name, key, ptype string, v any) error {
	switch ptype {
	case "integer":
		switch t := v.(type) {
		case float64:
			if t != float64(int64(t)) {
				return fmt.Errorf("argument %q for tool %s: expected an integer, got %v", key, name, t)
			}
		case int:
			// Go-API callers
		case string:
			if s := strings.TrimSpace(t); s != "" {
				if _, err := strconv.Atoi(s); err != nil {
					return fmt.Errorf("argument %q for tool %s: expected an integer, got %q", key, name, s)
				}
			}
		case nil:
			return fmt.Errorf("argument %q for tool %s: expected an integer, got null", key, name)
		case []string, []any:
			return fmt.Errorf("argument %q for tool %s: expected an integer, got an array", key, name)
		case map[string]any:
			return fmt.Errorf("argument %q for tool %s: expected an integer, got an object", key, name)
		default:
			return fmt.Errorf("argument %q for tool %s: expected an integer, got %T", key, name, v)
		}
	case "number":
		switch t := v.(type) {
		case float64, int:
			// JSON numbers always decode to float64; int covers Go-API callers.
		case string:
			if s := strings.TrimSpace(t); s != "" {
				if _, err := strconv.ParseFloat(s, 64); err != nil {
					return fmt.Errorf("argument %q for tool %s: expected a number, got %q", key, name, s)
				}
			}
		case nil:
			return fmt.Errorf("argument %q for tool %s: expected a number, got null", key, name)
		case []string, []any:
			return fmt.Errorf("argument %q for tool %s: expected a number, got an array", key, name)
		case map[string]any:
			return fmt.Errorf("argument %q for tool %s: expected a number, got an object", key, name)
		default:
			return fmt.Errorf("argument %q for tool %s: expected a number, got %T", key, name, v)
		}
	case "boolean":
		switch t := v.(type) {
		case bool:
		case float64:
			if t != 0 && t != 1 {
				return fmt.Errorf("argument %q for tool %s: expected a boolean, got a number", key, name)
			}
		case int:
			if t != 0 && t != 1 {
				return fmt.Errorf("argument %q for tool %s: expected a boolean, got a number", key, name)
			}
		case string:
			if s := strings.TrimSpace(t); s != "" {
				switch strings.ToLower(s) {
				case "true", "false", "1", "0":
				default:
					return fmt.Errorf("argument %q for tool %s: expected a boolean, got %q", key, name, s)
				}
			}
		case nil:
			return fmt.Errorf("argument %q for tool %s: expected a boolean, got null", key, name)
		case []string, []any:
			return fmt.Errorf("argument %q for tool %s: expected a boolean, got an array", key, name)
		case map[string]any:
			return fmt.Errorf("argument %q for tool %s: expected a boolean, got an object", key, name)
		default:
			return fmt.Errorf("argument %q for tool %s: expected a boolean, got %T", key, name, v)
		}
	}
	return nil
}

// validateArgTypes enforces the documented MCP argument-coercion contract
// (D6) at the dispatch choke point (runTool), per the tool's schema types.
// For type "string" properties: a present argument must be a JSON string, a
// JSON number (coerced to its canonical string form by argString —
// plugin/JSON callers legitimately pass numeric strings) or a JSON boolean
// (the stringly-typed boolean flags round-trip through argBool/argString).
// null, object and array values are rejected with a clear error naming the
// argument, the tool and the expected type, so query:{} or query:[...]
// surface as an isError instead of a silent "no symbols matched". For
// integer/number/boolean properties, validateNonStringScalar enforces the
// same fail-loud contract. Arguments not declared in the schema (agent_id,
// ...) and object/array-typed properties pass through untouched. Unknown
// tool names validate nothing. The tool's schema is resolved through the
// init-built toolByName map (O(1)) instead of a linear scan of the
// ~140-tool catalog per call.
func validateArgTypes(name string, args map[string]any) error {
	t, ok := toolByName[name]
	if !ok {
		return nil // unknown tool: nothing to validate
	}
	props, _ := t.InputSchema["properties"].(map[string]any)
	for key, raw := range props {
		prop, _ := raw.(map[string]any)
		ptype, _ := prop["type"].(string)
		v, ok := args[key]
		if !ok {
			continue // absent optional argument: nothing to coerce
		}
		if ptype == "" {
			// Union-typed property (W19, 2026-10-26 black-box audit):
			// e.g. kern_apply's edits accepts the array itself or its
			// JSON encoding as a string — the handlers are tolerant,
			// but this walk used to reject array callers before the
			// handler ever ran because the schema said "string" while
			// the description said "Array of ...". Accept any listed
			// type (integer and number match each other).
			if union, isUnion := prop["type"].([]any); isUnion {
				if err := validateUnionType(name, key, union, v); err != nil {
					return err
				}
				continue
			}
		}
		if ptype != "string" {
			if err := validateNonStringScalar(name, key, ptype, v); err != nil {
				return err
			}
			continue
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
	// M#18 (Phase 4.3): a root-less call resolves to the server cwd by design
	// for hosted sessions (the plugin and MCP hosts start the server inside
	// the project; the served-ledger etag keys on root-less digests), but a
	// scratch cwd must fail with a correctable error instead of silently
	// indexing it. Runs after the type walk (type errors take precedence) and
	// before the D1 cache gate, R7 slices, and dispatch — so no handler, no
	// index build, and no state writes touch the scratch directory.
	cwd, werr := os.Getwd()
	if werr != nil {
		cwd = ""
	}
	return validateRootFallback(name, props, args, cwd)
}

// validateUnionType enforces a JSON-Schema union-typed property: the value's
// JSON kind must be one of the listed types (integer and number match each
// other, mirroring the D6 lenience).
func validateUnionType(name, key string, union []any, v any) error {
	kind := jsonValueKind(v)
	for _, t := range union {
		s, _ := t.(string)
		if s == kind {
			return nil
		}
		if (s == "number" || s == "integer") && (kind == "number" || kind == "integer") {
			return nil
		}
	}
	var names []string
	for _, t := range union {
		if s, ok := t.(string); ok {
			names = append(names, s)
		}
	}
	return fmt.Errorf("argument %q for tool %s: expected one of types %s, got %s", key, name, strings.Join(names, " or "), kind)
}

// jsonValueKind reports the JSON kind of a decoded (or Go-API) argument value.
func jsonValueKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case []any, []string:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// validateRootFallback enforces the guarded cwd fallback for tools that
// declare a "root" argument. The fallback fires only when the caller omitted
// root or passed an explicit cwd form ("", ".", "./"); an explicit project
// path is the confinement gate's business. cwd must be a plausible project
// (git checkout or existing kern index) for the fallback to serve.
func validateRootFallback(name string, props map[string]any, args map[string]any, cwd string) error {
	if _, ok := props["root"]; !ok {
		return nil
	}
	if v, ok := args["root"]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr {
			return nil // non-string roots are rejected by the type walk
		}
		if t := strings.TrimSpace(s); t != "" && t != "." && t != "./" {
			return nil // explicit project path: confinement gate handles it
		}
	}
	if root.PlausibleProject(cwd) {
		return nil
	}
	return fmt.Errorf("argument %q for tool %s: root is required — the server working directory %q is not a project (no .git and no populated kern index: .kern/index.json or .kern/index.sqlite); pass the project path", "root", name, cwd)
}

// capProvenanceSymbolsToFit trims the structured Symbols listing so the serialized
// provenance stays within budgetBytes — the same per-call output budget
// (max_output, the per-tool table, KERN_MCP_MAX_OUTPUT) that bounds the
// content text. maxResponseSymbols caps the symbol COUNT, never bytes, so
// without this a small text budget still ships a listing that dwarfs the
// content it attests (measured: 500-byte budget, ~19KB symbols). The fixed
// header — schema, mode, authorizing rule, index identity — is the minimum
// viable attestation and always remains; only Symbols shrinks. A non-positive
// budget (sandbox disabled) is a no-op. SymbolsTruncated is set whenever any
// entry is dropped, mirroring the count cap above.
func capProvenanceSymbolsToFit(p *provenance.Provenance, budgetBytes int) {
	if p == nil || budgetBytes <= 0 || len(p.Symbols) == 0 {
		return
	}
	// The header is the payload with the listing dropped; SymbolsTruncated
	// forced true so its encoding is accounted for when entries survive.
	header := *p
	header.Symbols = nil
	header.SymbolsTruncated = true
	hb, err := json.Marshal(header)
	if err != nil {
		return
	}
	// Each entry is charged one byte beyond its own encoding for the comma
	// separator, and two bytes are reserved for the array wrapper, so the
	// final marshal cannot overshoot the budget.
	remaining := budgetBytes - len(hb) - 2
	if remaining < 0 {
		p.Symbols = nil
		p.SymbolsTruncated = true
		return
	}
	kept, used := 0, 0
	for _, s := range p.Symbols {
		b, err := json.Marshal(s)
		if err != nil || used+len(b)+1 > remaining {
			break
		}
		used += len(b) + 1
		kept++
	}
	if kept < len(p.Symbols) {
		p.Symbols = p.Symbols[:kept]
		p.SymbolsTruncated = true
	}
}
