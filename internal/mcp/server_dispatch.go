package mcp

import (
	"context"
	"encoding/json"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcpserve"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/serveoutput"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// dispatch computes the JSON-RPC response for a request. A nil return means
// the request needs no response (e.g. a notification). The response is a
// transport-neutral object so both stdio and HTTP can send it.
func (s *Server) dispatch(req rpcRequest) any {
	// A frame with no method that carries result/error is a client reply to a
	// server-initiated request (host sampling). The stdio loop delivers those
	// before dispatch; on HTTP, or when no sampler is waiting, JSON-RPC
	// defines no server-side error for an unsolicited response — drop it
	// silently. A frame with no method and no result/error is simply invalid:
	// -32600, not the old -32601 "method not found: " with an empty method
	// (envelope audit F-4, 2026-10-25). Response frames are exempt from the
	// pre-initialize gate below — they are replies, not session requests.
	if req.Method == "" {
		if rawPresent(req.Result) || rawPresent(req.Error) {
			s.deliverSamplingReply(req)
			return nil
		}
		return errorResponse(req.ID, -32600, "invalid request: missing method")
	}
	// KERN_MCP_ROOTS confinement gate (memory-#31): every tools/call whose
	// path-typed arguments (root, dir, or any key containing "path") resolve
	// outside the allowed roots is rejected here, before any handler runs.
	// The rejection uses the same result shape a handler error produces — a
	// tool result with isError=true — so the client sees a clean tool error
	// rather than a panic or a JSON-RPC error. A nil or disabled gate is a
	// no-op, keeping the default (loopback-client trust) behavior identical.
	// The tools/call params are decoded ONCE here and shared with the gate
	// check AND toolCallResponseDecoded below, so a call is JSON-decoded
	// exactly once (the gate previously re-unmarshalled what
	// toolCallResponse had already decoded). A malformed params body is not
	// a gate matter — the old gate decode silently skipped it and
	// JSON-RPC 2.0: a request id, when present, MUST NOT be null — only an
	// absent id makes a notification. An explicit "id":null on a request is
	// rejected as invalid so a client waiting on the response is answered
	// (-32600) instead of hanging forever (W9, 2026-10-26 black-box audit).
	// The notifications/ namespace is exempt: some hosts serialize
	// notifications with an explicit null id. Runs before the init gate: the
	// frame is protocol-invalid regardless of session state. Responses to
	// server-initiated requests (no method) are not affected.
	if req.Method != "" && req.hasExplicitNullID() && !strings.HasPrefix(req.Method, "notifications/") {
		return errorResponse(nil, -32600, "invalid request: id must not be null (send no id for notifications)")
	}
	// MCP initialization contract: the client must send initialize before
	// any other request. Requests arriving before that are rejected with
	// -32002; initialize itself is the entry point and exempt. LOW-10: this
	// gate runs AHEAD of the tools/call confinement pre-check below, so a
	// pre-init out-of-root tools/call sees the spec's -32002 (not the
	// governance denial). Notifications carry no id, so no response is
	// possible for them; they fall through to the silent-drop paths below.
	// `$/`-prefixed lifecycle methods (e.g. $/cancelRequest) are exempt too:
	// transport plumbing, not session tools — cancelling an in-flight call
	// must work regardless of the init flag's state.
	if req.Method != "initialize" && !s.initialized.Load() && !req.isNotification() && !strings.HasPrefix(req.Method, "$/") {
		return errorResponse(req.ID, -32002, "server not initialized: send the initialize request first")
	}
	// toolCallResponse answered "invalid params"; the tools/call case below
	// answers identically.
	var callParams toolCallParams
	callParamsOK := false
	if req.Method == "tools/call" {
		callParamsOK = json.Unmarshal(req.Params, &callParams) == nil
		if callParamsOK && s.gate != nil && s.gate.Enabled() {
			if err := s.gate.Check(callParams.Name, callParams.Arguments); err != nil {
				denied := "pre-tool-use denied: " + err.Error()
				result := map[string]any{
					"content": []any{map[string]any{"type": "text", "text": denied}},
					"isError": true,
				}
				mcpserve.AttachTokenMetadata(result, callParams.Name, callParams.Arguments, tokenize.Count(denied))
				return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
			}
		}
	}
	// $/cancelRequest is a notification per JSON-RPC (no response expected),
	// but must still be processed: dispatch returns nil for notifications
	// below, so it is handled here before the short-circuit. The MCP spec's
	// own spelling notifications/cancelled is accepted as an alias, with its
	// requestId param (envelope audit F-6, 2026-10-25).
	if req.Method == "$/cancelRequest" || req.Method == "notifications/cancelled" {
		var p struct {
			ID        json.RawMessage `json:"id"`
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		cancelID := p.ID
		if len(cancelID) == 0 {
			cancelID = p.RequestID // spec spelling: {"requestId": ..., "reason": ...}
		}
		key := idKey(cancelID)
		s.mu.Lock()
		cancel, ok := s.inflight[key]
		s.mu.Unlock()
		if ok && cancel != nil {
			cancel()
		}
		if req.isNotification() {
			return nil
		}
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	}
	if req.isNotification() {
		return nil
	}
	switch req.Method {
	case "initialize":
		// Initialization flips the pre-init gate: every request before this
		// one was rejected with -32002; from here on the full surface is
		// available. Set before any fallible work so a client that races its
		// next request sees a consistent view.
		s.initialized.Store(true)
		// Kick off background indexing so setup stays instant: graph tools
		// block until the build finishes, while tools that don't need the
		// index serve immediately. indexOnce guards the trigger so it fires
		// exactly once per server lifetime; serve paths already called
		// startPreload, making this a no-op fallback.
		s.startPreload()
		// ADR-0011 reactive file watcher: opt-in (KERN_MCP_WATCH_INTERVAL_MS,
		// default OFF), started alongside the preload and stopped on Close.
		s.startReactiveWatcher()
		caps := map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"prompts": map[string]any{"listChanged": false},
		}
		if s.transport == "http" {
			caps["streamableHttpCapabilities"] = map[string]any{"sse": false}
		}
		// Echo the client's negotiated protocol version when it is one we
		// support; otherwise report the version we implement.
		version := protocolVersion
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
			SchemaVersion   string `json:"schemaVersion"`
			ClientInfo      struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
			Capabilities struct {
				Sampling json.RawMessage `json:"sampling"`
			} `json:"capabilities"`
		}
		if json.Unmarshal(req.Params, &initParams) == nil {
			if supportedProtocolVersions[initParams.ProtocolVersion] {
				version = initParams.ProtocolVersion
			}
			// Host model delegation: a client that announces sampling
			// capability can serve LLM generation to kern (sampling/
			// createMessage round-trips). Register the host sampler so the
			// auto LLM chain's first leg is the connected host agent.
			if rawPresent(initParams.Capabilities.Sampling) {
				s.samplingMu.Lock()
				s.samplingCapable = true
				s.samplingMu.Unlock()
				s.registerHostSampling()
			}
		}
		// Negotiate the tool schema contract version (P2-003): honor a supported
		// client request verbatim, else serve the current catalog version. The
		// negotiated value is stored so tools/list can advertise the version the
		// client actually speaks. The client's self-reported name is stored
		// alongside it for per-tool stats attribution (explicit agent_id args
		// still win at the recording site).
		schemaVersion := negotiateSchemaVersion(initParams.SchemaVersion)
		s.mu.Lock()
		s.schemaVersion = schemaVersion
		s.clientName = initParams.ClientInfo.Name
		s.mu.Unlock()
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": version,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": serverName, "version": currentServerVersion()},
				"schemaVersion":   schemaVersion,
				"instructions":    serverInstructions(),
			},
		}
	case "notifications/initialized":
		return nil
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		// The catalog fits one page, so pagination is not implemented and no
		// cursor is ever emitted. A cursor supplied by the client is
		// therefore always unknown: reject it with -32602 instead of
		// silently ignoring it, so a buggy client cannot believe it is
		// paging when it is re-reading page one (W2, 2026-10-26 black-box
		// audit). nextCursor is OMITTED when there is no next page: the MCP
		// schema types it as an optional string, and strict clients (opencode
		// 1.19.4) reject an explicit null with "Failed to get tools".
		if req.hasExplicitCursor() {
			return errorResponse(req.ID, -32602, "invalid params: unknown or unsupported pagination cursor")
		}
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": s.filteredTools(), "schemaVersion": s.schemaVersionFor()}}
	case "tools/call":
		if !callParamsOK {
			return errorResponse(req.ID, -32602, "invalid params")
		}
		return s.toolCallResponseDecoded(req.ID, callParams)
	case "prompts/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"prompts": prompts}}
	case "prompts/get":
		return s.promptGetResponse(req.ID, req.Params)
	default:
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace. Delegates to fsutil.RootedPath.
func rootedPath(root, p string) (string, error) {
	return fsutil.RootedPath(root, p)
}

func (s *Server) runTool(ctx context.Context, id, token, name string, args map[string]any) (out string, runErr error) {
	// Record every incoming tool call and its duration; the defer covers all
	// return paths, and runErr is non-nil exactly when dispatch returned an error.
	origName := name // precheckTool may remap the name; audit the executed one
	metrics.Default().RecordRequest()
	start := time.Now()
	fromCache := false
	// skipFooter marks the etag short-circuit returns below: their tiny
	// protocol payloads ("unchanged (etag …)") are matched verbatim by
	// callers and must not gain a duration footer line.
	skipFooter := false
	defer func() {
		took := time.Since(start)
		// D1: cache-hit durations must not pollute the RecordToolCall latency
		// percentiles — hits are counted under RecordCacheHit instead (F4).
		if !fromCache {
			metrics.Default().RecordToolCall(took)
		}
		if runErr != nil {
			metrics.Default().RecordError()
		}
		// every MCP tool execution — read-only tools included —
		// appends to the tamper-evident audit chain. Pre-dispatch
		// rejections (allowlist / root validation) are recorded as
		// blocked; executed calls as allowed/error. Cache hits append too,
		// tagged Policy:"tool-cache" / Reason:"served from cache" (F5).
		if name != "" {
			s.auditToolCall(name, args, runErr, true, fromCache, took)
		} else {
			s.auditToolCall(origName, args, runErr, false, fromCache, took)
		}
		// Duration visibility L1: the host UI shows the call's wall-clock to
		// the HUMAN from its own part timestamps, but the MODEL only sees the
		// result text — and some hosts (opencode) discard progress
		// notification content entirely. A threshold-gated last line makes
		// the latency model-visible on every host. Fast calls and error
		// results stay byte-identical to today; cache hits are fast and the
		// etag short-circuits are exempted via skipFooter.
		if runErr == nil && !skipFooter {
			out = appendDurationFooter(out, took)
		}
	}()
	// B1 (ADR-0012): conditional-fetch state is strictly per-call. runTool
	// re-enters under the SAME ctx for composed pipeline steps (kern_compose
	// -> mcpcompose.Compose -> runTool per step), so the scope must be reset
	// at the top of every execution — otherwise step N inherits step N-1's
	// etag and a caller passing step N-1's etag in step N's args wrongly
	// receives "unchanged" even when step N's content changed (HIGH-1). The
	// D1 cache-hit path re-stamps scope.etag from the stored entry below, and
	// maybeShortCircuit re-stamps it for fresh dispatches, so resetting here
	// is safe for every path.
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		scope.etag = ""
		scope.unchanged = false
		scope.sliced = false
		scope.routed.routedSet = false
	}
	name, err := s.precheckTool(name, args)
	if err != nil {
		return "", err
	}
	// D6: enforce the string-argument coercion contract (accept string/number/
	// bool; reject null/object/array) before the handler runs, so a wrong-typed
	// argument surfaces as a clear isError instead of a silent mis-coercion.
	if err := validateArgTypes(name, args); err != nil {
		return "", err
	}
	// R7 "more" cursor (see retain.go): a slice=<anchor>:lines:A-B|tail:N
	// re-call reads a sandbox-truncated response's ELIDED part from the
	// retained-output store WITHOUT executing the tool. Intercepted here —
	// before the D1 cache gate and dispatch — so a slice request can never
	// reach tool execution. precheckTool and validateArgTypes still gate it
	// (F4: like a cache hit, it never skips allowlist/root validation/budget),
	// and the audit/metrics deferred above record the read. The response is
	// still bounded by the output budget (A3): toolCallResponse applies the
	// sandbox to sliced responses too and, when truncation bites, mints a
	// FRESH anchor for the elided remainder so the recovery path CHAINS
	// instead of flooding (scope.sliced tells it to retain only the remainder,
	// never the whole retained entry).
	if spec := argString(args, "slice"); spec != "" {
		if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
			scope.sliced = true
		}
		out, serr := serveRetainedSlice(spec)
		if serr != nil {
			return "", serr
		}
		return out, nil
	}
	// kern_meta D1 cache routing: resolve the routed (name, args) ONCE for
	// this call and memoize it on the per-call scope, so cacheableForCall
	// (both the lookup and store gates below) and cacheKeyFor (in cacheLookup
	// and cacheStore, including the F-1 fingerprint path) read the memo
	// instead of re-running the classifier up to 4× on the same request.
	// resolveCacheCall is pure and deterministic, so the memo is byte-identical
	// to what each consumer would have computed. Placed after the R7 slice
	// re-read (which never touches the cache) and reset at the top of every
	// runTool entry so composed steps (same scope, different args) recompute.
	if name == "kern_meta" {
		if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
			scope.routed.name, scope.routed.args = resolveCacheCall(context.Background(), name, args)
			scope.routed.routedSet = true
		}
	}
	// Loop-suspect detection (B4, anti-stall campaign Phase 1): observe the
	// read query EARLY — before the served-ledger pointer and D1 gates — so
	// the ring records every read call, including ones whose payload gets
	// deduped (a loop is still a loop when the answer was re-served). The
	// advisory marker is appended only to full-payload responses below;
	// pointer lines, etag short-circuits, slice re-reads and error returns
	// never carry it.
	loopMarker := s.loopObserve(ctx, name, args)
	// Served ledger (B1, anti-stall campaign Phase 0): per-session
	// already-served dedup for read tools. When the same (tool,
	// view-defining args) was already served in THIS session, answer with a
	// one-line pointer instead of re-emitting the payload — protecting the
	// agent's context window and teaching the etag in-band. Placed after the
	// R7 slice re-read and the kern_meta routed memo (the ledger keys the
	// ROUTED call, same resolution as the D1 cache), before the D1 gate: a
	// ledger hit is cheaper than even a cache hit. no_cache=1 bypasses
	// (fresh serve, same as D1), and so does an explicit etag (the caller is
	// already speaking the conditional-fetch protocol — the D1 path serves
	// the tiny "unchanged" reply, which is what the pointer teaches).
	if ptr, ok := s.ledgerLookup(ctx, name, args); ok {
		skipFooter = true
		return ptr, nil
	}
	// D1 — tool-response cache: check INSIDE runTool, after precheckTool and
	// validateArgTypes succeed and before dispatchTool (F4), so a cache hit
	// never skips the KERN_TOOLS allowlist, root validation, the 	// safety budget, metrics or the audit chain. Only Cacheable (explicit
	// opt-in allowlist, F1) tools participate; KERN_MCP_CACHE=0 and per-call
	// no_cache=1 bypass. The effective flag is call-level (cacheableForCall):
	// kern_meta participates only when the sub-tool it routes to is itself
	// cacheable (R1), and semantic calls never participate (R3). Lookup and
	// store below use the same gate, so both paths are covered.
	if cacheableForCall(ctx, name, args) && cacheEnabled() && !noCacheArg(args) {
		if text, hit := s.cacheLookup(ctx, name, args); hit {
			fromCache = true
			metrics.Default().RecordCacheHit()
			// ETag conditional-fetch (ADR-0012): a cache hit is the ideal
			// short-circuit site — serving from cache then answering
			// "unchanged" is expected, and the short response is never
			// stored back.
			if short := s.maybeShortCircuit(ctx, name, args, text); short != "" {
				skipFooter = true
				return short, nil
			}
			// Served ledger: a full-payload cache-hit replay is a serve —
			// record it (with the entry's etag, stamped on the scope by
			// cacheLookup) so an identical repeat in this session gets the
			// one-line pointer.
			s.ledgerRecord(ctx, name, args, text)
			hint := s.hintFor(ctx, name, args, text, loopMarker)
			text = appendLoopMarker(text, loopMarker)
			text = appendHintLine(text, hint)
			return text, nil
		}
		metrics.Default().RecordCacheMiss()
	}
	// C2/F4 (W6): pre-render short-circuit — a registry-verified etag claim
	// with unchanged index identity and serve view proves the response would
	// be byte-identical, so the handler render is skipped. Placed after the
	// D1 gate (a cache hit already avoids the render) and before the progress
	// wrap (no handler, no progress). Falls back to the post-render
	// maybeShortCircuit below on any mismatch.
	if short := s.maybePreRenderShortCircuit(ctx, name, args); short != "" {
		skipFooter = true
		return short, nil
	}
	// slow tools emit MCP progress notifications (0% start, 5s
	// keep-alive, 100% stop) so agents see liveness instead of silence during
	// multi-second tool calls. Centralized here (sandbox/heal/run_build
	// previously started progress in their handlers; the central wrap
	// supersedes those). Cache hits return before this point, so they never
	// emit progress. Progress is only emitted when the client supplied a
	// progressToken in the request's _meta (token != ""); a call without one
	// gets no unsolicited notifications.
	if slowTools[name] && token != "" {
		stop := s.startProgress(ctx, id, token, name)
		defer stop()
		// Stage-aware progress on top of the generic 5s keep-alive:
		// loop-family tools (kern_loop, kern_do, and anything routed
		// through the loop engine) report each stage as it starts
		// ("stage: verify", with cumulative pct). The reporter travels on
		// ctx; the loop engine reads it between stages — no signature
		// changes through TaskService/highlevel.
		ctx = loop.WithStageReporter(ctx, func(stage string, pct int) {
			s.progress(ctx, token, name, pct, "stage: "+stage)
		})
	}
	text, err := s.dispatchTool(ctx, id, name, args)
	// ETag conditional-fetch (ADR-0012): the etag covers the RAW pre-sandbox
	// response text folded with the serve-time view (max_output budget, F-2),
	// so the short circuit runs before the D1 store. A short-circuited
	// response must NOT enter the D1 cache (item 2) — the return below skips
	// cacheStore.
	if err == nil {
		if short := s.maybeShortCircuit(ctx, name, args, text); short != "" {
			skipFooter = true
			return short, nil
		}
		// D1: store only successful (non-error) results; the response text is
		// the raw pre-sandbox output (max_output is applied at serve time).
		if cacheableForCall(ctx, name, args) && cacheEnabled() && !noCacheArg(args) {
			s.cacheStore(ctx, name, args, text)
		}
		// Served ledger: record the full-payload serve with the etag minted
		// for it (scope.etag), so an identical repeat in this session is
		// answered with the one-line pointer instead of the payload.
		s.ledgerRecord(ctx, name, args, text)
		hint := s.hintFor(ctx, name, args, text, loopMarker)
		text = appendLoopMarker(text, loopMarker)
		text = appendHintLine(text, hint)
	}
	return text, err
}

// maybeShortCircuit implements the ETag conditional-fetch contract for one
// tool response (ADR-0012 items 1-2): for an eligible read tool it computes
// the response etag from the raw text, the tool's schema version and the
// serve-time view (max_output budget, F-2), stamps it on the per-call scope
// (so toolCallResponse attaches result["etag"] and records the working set),
// and when the caller passed etag=<previous> equal to the fresh value returns
// the tiny unchanged response. It returns "" when the tool is not eligible or
// the caller's etag does not match (the full text must be served). The
// unchanged verdict is carried on the scope so the response also carries
// result["unchanged"]=true.
func (s *Server) maybeShortCircuit(ctx context.Context, name string, args map[string]any, text string) string {
	if !etag.Eligible(name) {
		return ""
	}
	// The conditional-fetch protocol is carried on the per-call indexScope:
	// toolCallResponse attaches result["etag"]/result["unchanged"] and records
	// the working set from that same scope. The RAW governed passthrough
	// (CallToolGoverned — string-only SDK callers) runs runTool with the
	// caller's bare ctx — no scope — so it cannot speak the protocol; a
	// matching etag would otherwise render the literal "unchanged (etag E)"
	// string as the ENTIRE tool output for a string-only consumer. The
	// structured variant (CallToolGovernedResult, C2/F3 — REST routes, SDK
	// CallResult) attaches a per-call scope, carries etag/unchanged as
	// structured fields, and records the serve exactly like a tools/call.
	// Etags are only ever computed when a scope exists to carry them.
	scope, hasScope := ctx.Value(indexScopeKey{}).(*indexScope)
	if !hasScope {
		return ""
	}
	// A D1 cache hit replays the entry's stored etag (computed from the raw
	// pre-mask text), so it wins over a recompute over the replayed bytes.
	e := ""
	if scope.etag != "" {
		e = scope.etag
	} else {
		// F-2: the etag is bound to the serve-time view — the call's
		// effective max_output budget. A malformed budget errors the call
		// downstream in toolCallResponse; minting with -1 keeps the hash
		// deterministic here (and cacheStore's fallback uses the identical
		// computation, so fallback and short-circuit agree).
		budget, err := serveoutput.CallOutputBudget(name, args)
		if err != nil {
			budget = -1
		}
		e = etag.HashView(text, toolByName[name].SchemaVersion, strconv.Itoa(budget))
		scope.etag = e
	}
	prev := argString(args, "etag")
	if prev != "" && prev == e {
		// Trust-but-verify (C1/P1): only an agent the working-set registry
		// shows was actually handed this etag for these args may collapse
		// the re-read. A caller claiming an etag it never received — a
		// different agent sharing a client-side cache, or a foreign etag —
		// gets the full text: content over token savings (operator
		// decision, reject strictness). The stateless CLI conditional fetch
		// (clietag) never consults this; the check applies only where the
		// protocol is scope-carried. toolCallResponse re-records the entry
		// on this serve, so an LRU-evicted agent recovers after one full
		// re-serve.
		if served, ok := etag.Default.ServedETag(argString(args, "agent_id"), etag.CanonicalArgsKey(name, args)); !ok || served != e {
			return ""
		}
		scope.unchanged = true
		return etag.UnchangedResponseText(e)
	}
	return ""
}

// serveView returns the serve-time view component the response etag is
// bound to (F-2): the tool's catalog schema version and the call's
// effective max_output budget. Identical to what maybeShortCircuit folds
// into HashView, so a view recorded at serve time and recomputed at claim
// time are directly comparable (C2/F4).
func serveView(name string, args map[string]any) string {
	budget, err := serveoutput.CallOutputBudget(name, args)
	if err != nil {
		budget = -1
	}
	return toolByName[name].SchemaVersion + ":" + strconv.Itoa(budget)
}

// maybePreRenderShortCircuit implements the pre-render half of the
// conditional-fetch contract (C2/F4, W6): when the caller's etag claim is
// registry-verified (trust-but-verify, C1/P1) AND the index identity and
// the serve view are unchanged since that etag was minted, the response is
// provably identical, so the handler render is skipped entirely — saving
// the compute cost of a full re-render, not just the response tokens. It
// runs after the D1 gate (a cache hit already skips the render) and after
// precheckTool/validateArgTypes — it never bypasses the allowlist, root
// validation or the audit chain; it only answers a verified re-read. Any
// mismatch (foreign etag, evicted entry, content change — the probe-gated
// identity from C2/F1 rebuilds on staleness — or view drift) falls back to
// the post-render maybeShortCircuit. Returns "" when the full render must
// run.
func (s *Server) maybePreRenderShortCircuit(ctx context.Context, name string, args map[string]any) string {
	if !etag.Eligible(name) {
		return ""
	}
	prev := argString(args, "etag")
	if prev == "" {
		return ""
	}
	scope, ok := ctx.Value(indexScopeKey{}).(*indexScope)
	if !ok {
		return ""
	}
	entry, ok := etag.Default.ServedEntry(argString(args, "agent_id"), etag.CanonicalArgsKey(name, args))
	if !ok || entry.ETag != prev || entry.Identity == "" || entry.View == "" {
		return ""
	}
	// The index identity must still be the one the etag was minted under.
	// cacheIndexIdentity is probe-gated (C2/F1): a content change anywhere
	// in the tree produces a different identity (or "noindex" when the
	// session must rebuild) and forces the full render. Within the
	// freshness-probe cooldown this can serve a stale-identity "unchanged" —
	// the same bounded window the post-render short-circuit already has
	// (ADR-0012 race notes).
	root := resolveRoot(argString(args, "root"))
	if cur := s.cacheIndexIdentity(root); cur == "" || cur != entry.Identity {
		return ""
	}
	if v := serveView(name, args); v != entry.View {
		return ""
	}
	scope.etag = prev
	scope.unchanged = true
	return etag.UnchangedResponseText(prev)
}

// analyzeChange and simulateChange have been migrated to internal/app.Platform.
// The MCP handlers now call app.New(root) + p.Analyze/WhatIf so the
// orchestration is shared with CLI and REST instead of duplicated here.

// changedContext resolves the changed files for a tool call: an explicit
// comma-separated file list wins; otherwise the git range (empty = working
// tree). Returns line-aware FileChanges so blast radius can be scoped to the
// changed hunks.
func (s *Server) changedContext(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
	root := resolveRoot(argString(args, "root"))
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if files := argString(args, "file"); files != "" {
		var out []intel.FileChange
		for _, p := range strings.Split(files, ",") {
			if p = strings.TrimSpace(p); p != "" {
				resolved, err := withinRoot(root, p)
				if err != nil {
					return nil, nil, err
				}
				rel, err := filepath.Rel(root, resolved)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, intel.FileChange{File: rel})
			}
		}
		return out, ix, nil
	}
	from, to := "", ""
	if r := argString(args, "range"); r != "" {
		if p := strings.SplitN(r, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			from = r
		}
	}
	changes, err := intel.FilesForRangeL(root, from, to)
	if err != nil {
		return nil, nil, err
	}
	return changes, ix, nil
}
