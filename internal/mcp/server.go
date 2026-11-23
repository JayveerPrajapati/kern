// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"context"
	"encoding/json"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/govaudit"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"github.com/JayveerPrajapati/kern/internal/mcpgate"
	"github.com/JayveerPrajapati/kern/internal/mcphints"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/servedledger"
	"github.com/JayveerPrajapati/kern/internal/session"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tool, tools and ToolNames re-export the catalog package for callers that
// predate the catalog extraction (handlers, transports, tests, setup parity).
type Tool = catalog.Tool

// tools is the registered catalog; the alias keeps in-package references
// (len(tools), range tools) stable while the table lives in catalog.All.
var tools = catalog.All

// ToolNames returns every registered MCP tool name (delegates to catalog).
func ToolNames() []string { return catalog.ToolNames() }

// Phase/risk/schema-version constants re-exported for the same reason.
const (
	PhaseExplore = catalog.PhaseExplore
	PhasePlan    = catalog.PhasePlan
	PhaseEdit    = catalog.PhaseEdit
	PhaseVerify  = catalog.PhaseVerify
	PhaseMeta    = catalog.PhaseMeta
	PhaseCross   = catalog.PhaseCross

	RiskLow      = catalog.RiskLow
	RiskMedium   = catalog.RiskMedium
	RiskHigh     = catalog.RiskHigh
	RiskCritical = catalog.RiskCritical

	SchemaVersionV1      = catalog.SchemaVersionV1
	SchemaVersionCurrent = catalog.SchemaVersionCurrent
)

// supportedSchemaVersions lists every tool schema contract version the
// server can serve. A client that negotiates one of these versions gets
// tool contracts that match its expectations exactly.
var supportedSchemaVersions = map[string]bool{
	SchemaVersionV1: true,
}

// validSchemaVersion reports whether v is a well-formed tool schema version:
// a semantic version of the form major.minor.patch (e.g. "1.0.0"), all three
// components present and numeric. Negotiation compares versions lexically, so
// a malformed version would mis-order and is rejected up front.
func validSchemaVersion(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// negotiateSchemaVersion picks the schema version the server serves to a
// client that requested v during initialize (P2-003). The rule mirrors
// protocol version negotiation: a supported request is honored verbatim,
// while an empty or unsupported request falls back to the current catalog
// version. A client that asks for a version this server cannot serve detects
// the fallback by comparing the initialize response's schemaVersion with what
// it asked for.
func negotiateSchemaVersion(requested string) string {
	if supportedSchemaVersions[requested] {
		return requested
	}
	return SchemaVersionCurrent
}

// NOTE: KERN_MCP_PHASE and KERN_MCP_CATEGORY shape the advertised surface.
// Direct tools/call requests for unadvertised tools are rejected
// (advertised == invocable) unless KERN_MCP_ALLOW_UNADVERTISED=1 is set.
// kern_meta intentionally routes within the advertised sub-tool surface, so natural-
// language access to the full catalog remains available. KERN_TOOLS is the
// real execution allowlist for both direct calls and kern_meta routing.

// The bounded project-session LRU lives in internal/session (Cache). The
// server wires it with session.MaxSessions / session.SessionIdleEvict and an
// onEvict callback (evictSessionState) that drops the root-owned platform
// cache entries; see newServerCore.

// Server handles MCP requests over a stdio stream or HTTP.
type Server struct {
	in        io.Reader // raw stdio reader, used to rebuild the scanner after an oversized line
	out       io.Writer
	mu        sync.Mutex
	toolsMu   sync.Mutex
	filtered  []Tool   // cached KERN_TOOLS-filtered tool list (nil = not computed)
	allowlist []string // parsed KERN_TOOLS allowlist, cached once at init (nil = allow all)
	locks     map[string]*lock.Lock
	inflight  map[string]context.CancelFunc
	// sessCache is the bounded LRU of project sessions keyed by resolved
	// root (internal/session.Cache). Every distinct root accumulates a
	// project.Session (a full in-memory index plus an fswatch subprocess
	// each); the LRU bounds the map and evicts the least-recently-used idle
	// session beyond the cap, closing its watcher and releasing its index
	// (see sessionFor).
	sessCache *session.Cache
	// platforms caches one application Platform per project root, keyed to
	// the exact index instance it was built from. High-level handlers used to
	// rebuild the whole Platform (call graph + 4 twin extractors, each a full
	// tree walk) on every tool call; the cache reuses it while the session
	// serves the same index instance and rebuilds only after a real index
	// rebuild (new instance pointer).
	platforms map[string]*platformEntry
	// platformLocks serializes the per-root Platform build so two concurrent
	// first-calls for the same root cannot both run app.NewWithIndex (several
	// full tree walks each) and discard the loser. One mutex per root, created
	// lazily under s.mu and dropped alongside the platforms entry on LRU
	// eviction. Only the build holds it; cache lookups stay on s.mu.
	platformLocks map[string]*sync.Mutex
	transport     string // "stdio" (default) or "http"
	// schemaVersion is the tool schema contract version (P2-003) negotiated
	// during initialize: the client's requested version when supported,
	// else SchemaVersionCurrent. Empty until the first initialize; getters
	// fall back to SchemaVersionCurrent so tools/list is still correct when
	// a client skips the handshake.
	schemaVersion string
	// clientName is the MCP client's self-reported identity
	// (initialize params clientInfo.name, e.g. "opencode", "claude",
	// "cursor"). Captured at the initialize handshake, mutex-guarded like
	// the schemaVersion state; empty until a client initializes. Used for
	// per-tool stats attribution so `kern stats --by-agent` no longer
	// buckets every entry as "(unattributed)".
	clientName string
	// sem bounds how many tool calls may build an index concurrently (each
	// call can construct a full project index). Acquired before a tools/call
	// goroutine is spawned and released when it finishes.
	sem chan struct{}
	// roots confine every tool root/dir argument; KERN_ROOTS when set, else
	// the server's startup directory.
	roots []string
	// gate confines every tool call's path-typed arguments (root, dir, or any
	// key containing "path") to the KERN_MCP_ROOTS roots, resolving symlinks
	// before containment. A nil gate preserves the default behavior exactly.
	gate *mcpgate.Gate
	// commits caches the short HEAD commit per project root so git is spawned
	// at most once per root per server lifetime.
	commits map[string]string
	// indexOnce defers background index preloading until the first MCP
	// request (initialize or tools/call) instead of server startup, so
	// setup is instant and the index cost is paid while the user waits.
	indexOnce sync.Once
	// preloadWG tracks the background preload goroutines so Close can drain
	// them before closing sessions; closing (guarded by mu) stops new ones
	// from starting once shutdown has begun.
	preloadWG sync.WaitGroup
	closing   bool
	// indexedRoots records which project roots have completed at least one
	// index build this process, so the "first build in progress" notice is
	// printed once per root instead of on every stale rebuild.
	indexedRoots sync.Map
	// ADR-0011 reactive file watcher (opt-in, KERN_MCP_WATCH_INTERVAL_MS):
	// liveIndexes holds the atomically-published fresh index instances per
	// workspace root, swapped in by the background watcher after a rebuild so
	// handlers keep serving the old instance until the swap (per-call reads see
	// either the old or the new instance, never a half-built one). watcherOnce
	// guards the start, fired with indexOnce on the first initialize;
	// watcherCancel stops the poll loops on Close; watcherDone is closed once
	// they have drained.
	liveIndexes   sync.Map // resolved root -> *atomic.Pointer[index.Index]
	watcherOnce   sync.Once
	watcherCancel context.CancelFunc
	watcherDone   chan struct{}
	// audit records every executed (and pre-dispatch-rejected) MCP tool
	// call into the project's tamper-evident audit chain; auditMu
	// guards toolAudit's lazy initialization.
	audit   *govaudit.AuditLog
	auditMu sync.Mutex
	// preTool is an optional hook invoked before every tools/call execution.
	// It receives the tool name and its arguments and returns nil to allow the
	// call or an error to deny it (denial surfaces as a tool error response,
	// isError=true, no side effects). A nil hook preserves the default
	// behavior exactly — callers that never set it see zero change.
	preTool func(name string, args map[string]any) error

	// initialized flips when the initialize request has been processed.
	// Before it, every other request is rejected with -32002 per the MCP
	// contract; notifications cannot be answered and are dropped by the
	// notification short-circuit above the switch. Atomic because the
	// HTTP transport handles requests concurrently.
	initialized atomic.Bool
	// gateway enforces the safety-budget dimension of ToolGateway at the
	// tool-call choke point (precheckTool): every tools/call — direct and
	// compose steps alike — is tracked against budget before dispatch, and
	// denied with a structured error once the budget is exceeded. It is
	// budget-only by construction (NewToolGateway(nil): per-call firewalls
	// are built separately in newGovernor, so no firewall is held here). A
	// nil gateway is a safe no-op — callers that never wire one (or that
	// explicitly opt out via WithToolGateway(nil, nil)) see byte-for-byte
	// legacy behavior. budget is the gateway's working budget; nil disables
	// budget enforcement too.
	gateway *governance.ToolGateway
	budget  *domain.SafetyBudget
	// budgetMu serializes budget accounting in precheckTool. tools/call
	// requests are dispatched concurrently (Serve spawns one goroutine per
	// call), and domain.SafetyBudget's counters are not internally
	// synchronized, so the check-then-track critical section must be guarded
	// here. Under contention the mutex still guarantees at most MaxToolCalls
	// successful dispatches.
	budgetMu sync.Mutex
	// Background index watch: an implicit poll loop that rebuilds stale
	// workspace-root indexes between tool calls via the same on-demand path
	// (project.Session.Index). watchStop is closed by Close to halt the loop;
	// watchDone is closed by the loop when it exits; watchWG tracks in-flight
	// rebuilds so Close can drain one; watchMu/watchBusy implement
	// single-flight. See StartBackgroundWatch.
	watchStop    chan struct{}
	watchDone    chan struct{}
	watchOnce    sync.Once
	watchStarted sync.Once
	watchWG      sync.WaitGroup
	watchMu      sync.Mutex
	watchBusy    bool
	watchStopped bool
	// Host-model sampling (host agent delegation): when a stdio client
	// declares sampling capability at initialize, the server registers an
	// LLM host sampler (llm.RegisterHostSamplerFor, keyed by samplerKey) whose
	// Generate performs an MCP sampling/createMessage round-trip to the
	// connected host — the last-resort leg of the auto LLM chain. Explicit
	// command samplers (kern_register_host_sampler) register under their own
	// keys (default: this server's slot), so several agents/repos can coexist
	// without clobbering each other; every registered sampler is tried in
	// registration order. samplingSlots maps key -> disposer; samplingMu
	// guards it plus the pending correlation map and request counter.
	samplingMu      sync.Mutex
	samplingSeq     int64
	samplingPending map[string]chan samplingReply
	samplingSlots   map[string]func()
	samplingHost    bool // this server's own slot has a sampler (MCP sampling or command)
	samplingCapable bool
	samplerKey      string
	// ledger is the per-session served ledger (B1, anti-stall campaign Phase
	// 0): the already-served dedup store for read tools. One ledger per
	// Server = one ledger per MCP session, so repeats within a session are
	// caught without cross-session (cross-client, cross-test) pollution.
	// Wired in runTool; nil-safe (hand-rolled test servers degrade to a
	// miss/no-op).
	ledger *servedledger.Ledger
	// loopdetect is the per-session degenerate-loop detector (B4, anti-stall
	// campaign Phase 1): advisory near-duplicate detection for read queries.
	// One detector per Server = one per MCP session, like the ledger; wired
	// in runTool. Nil-safe (hand-rolled test servers degrade to a no-op).
	loopdetect *semcache.LoopDetector
	// phaseBudgetCap is the per-session explore-phase tool-call cap
	// (KERN_EXPLORE_BUDGET=N, N >= 1; 0 = disabled). phaseBudget counts
	// explore-phase calls per session key (sessionKeyFor), so the cap is
	// per-session-key and INDEPENDENT of s.budget (which is per-Server and,
	// where tasklife uses it, task-scoped). Guarded by budgetMu in
	// precheckTool (the same check-then-track critical section as the safety
	// budget).
	phaseBudgetCap int
	phaseBudget    map[string]int
	// hints is the per-session tool-adoption hint engine (B6, anti-stall
	// campaign Phase 4): in-band nudges inside tool responses reminding the
	// agent of high-value tools it never calls. One engine per Server = one
	// per MCP session, like the ledger; wired at the full-payload return
	// sites in runTool. Nil-safe (hand-rolled test servers degrade to a
	// no-op).
	hints *mcphints.Hints
}

// WithPreToolHook registers a pre-tool-use hook. NewServer wires the
// KERN_MCP_ROOTS confinement gate as the default hook (opt out via
// KERN_MCP_NO_CONFINE=1); calling WithPreToolHook replaces that default with
// the caller's own governance/allowlist/accounting hook. A nil hook leaves
// every tools/call untouched, so callers that explicitly pass nil get the
// fully default behavior.
func (s *Server) WithPreToolHook(fn func(name string, args map[string]any) error) *Server {
	s.preTool = fn
	return s
}

// WithToolGateway wires (or overrides) the safety-budget ToolGateway used at
// the tool-call choke point. A nil gateway plus a nil budget restores the
// default no-op mode (byte-for-byte legacy behavior). When gw is non-nil and
// budget is nil, the conservative domain.DefaultSafetyBudget() is used; a
// budget configured via KERN_SAFETY_BUDGET_* at construction is replaced by
// the one passed here. The gateway is budget-only by design (NewToolGateway
// with a nil firewall): per-call firewalls are built in newGovernor, so no
// global firewall state is held.
func (s *Server) WithToolGateway(gw *governance.ToolGateway, budget *domain.SafetyBudget) *Server {
	s.gateway = gw
	if gw != nil && budget == nil {
		d := domain.DefaultSafetyBudget()
		budget = &d
	}
	s.budget = budget
	return s
}

// safetyBudgetFromEnv builds the MCP safety budget from KERN_SAFETY_BUDGET_*
// environment overrides, falling back to the conservative
// domain.DefaultSafetyBudget() for every dimension that is unset or
// unparseable (a malformed override must never widen a limit). Supported:
// KERN_SAFETY_BUDGET_MAX_TOOL_CALLS, _MAX_FILES, _MAX_TOKENS,
// _MAX_EXTERNAL_CALLS, _MAX_COST, _MAX_RUNTIME_SECONDS, _MAX_RISK.
// safetyBudgetEnvSet reports whether the operator opted into the MCP
// safety budget by setting any KERN_SAFETY_BUDGET_* variable.
func safetyBudgetEnvSet() bool {
	for _, name := range []string{
		"KERN_SAFETY_BUDGET_MAX_TOOL_CALLS",
		"KERN_SAFETY_BUDGET_MAX_FILES",
		"KERN_SAFETY_BUDGET_MAX_TOKENS",
		"KERN_SAFETY_BUDGET_MAX_EXTERNAL_CALLS",
		"KERN_SAFETY_BUDGET_MAX_COST",
		"KERN_SAFETY_BUDGET_MAX_RUNTIME_SECONDS",
		"KERN_SAFETY_BUDGET_MAX_RISK",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func safetyBudgetFromEnv() *domain.SafetyBudget {
	d := domain.DefaultSafetyBudget()
	b := &d
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			b.MaxToolCalls = n
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_FILES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			b.MaxFiles = n
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			b.MaxTokens = n
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_EXTERNAL_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			b.MaxExternalCalls = n
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_COST"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			b.MaxCost = f
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_RUNTIME_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			b.MaxRuntime = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("KERN_SAFETY_BUDGET_MAX_RISK"); v != "" {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case string(domain.RiskLow), string(domain.RiskMedium), string(domain.RiskHigh), string(domain.RiskCritical):
			b.MaxRisk = domain.RiskLevel(strings.ToUpper(strings.TrimSpace(v)))
		}
	}
	return b
}

// defaultConcurrency returns the worker concurrency limit for the server.
// Configurable via KERN_MCP_CONCURRENCY (default 16, minimum 8). The default
// was lowered from 32 to 16: each concurrent tool call can hold an in-flight
// index build (a large memory ceiling), so halving the cap bounds peak memory
// on bursty multi-call workloads.
func defaultConcurrency() int {
	if v := os.Getenv("KERN_MCP_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 8 {
			return n
		}
	}
	return 16
}

// newServerCore constructs the transport-independent core of a Server: the
// KERN_TOOLS allowlist, the sampling slots, the confinement gate wiring, the
// opt-in safety-budget gateway, and host-sampler registration. Both
// transports (stdio NewServer and HTTP ServeHTTPContextWithTLS) must build
// through it — the HTTP path previously hand-rolled its own struct literal,
// silently skipping allowlist/samplerKey/samplingSlots, which made
// engine-backed tools (memory, security) panic and KERN_TOOLS a no-op over
// HTTP.
func newServerCore(transport string) *Server {
	// Inject the live tool catalog into the diff-gate drift checks explicitly
	// at server construction —
	// never via init(). cli (which cannot import mcp) reads it through
	// diffgate.ToolInfos() when building the diff-gate check list; a binary
	// that never constructs a server (e.g. a pure `kern diff-gate` CLI run)
	// is wired in cmd/kern main.
	catalog.WithDiffgateTools()
	s := &Server{sem: make(chan struct{}, defaultConcurrency()), locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}, transport: transport, roots: defaultWorkspaceRoots(), gate: confinementGate(), commits: map[string]string{}, allowlist: parseAllowlist(), watchStop: make(chan struct{}), watchDone: make(chan struct{}), samplerKey: samplerKeyFor(), samplingSlots: map[string]func(){}, ledger: servedledger.New(), loopdetect: semcache.NewLoopDetector(), phaseBudget: map[string]int{}, hints: mcphints.New()}
	// The explore-phase budget (B2) is opt-in via KERN_EXPLORE_BUDGET; unset
	// leaves phaseBudgetCap 0 (completely off, zero behavior change).
	s.phaseBudgetCap = exploreBudgetCap()
	// The session LRU is wired with the root-owned platform-cache cleanup as
	// the eviction callback: when an idle session is evicted, the per-root
	// Platform (full call graph + twin-merged knowledge state) and its build
	// lock go with it, so a long-lived server touching many distinct roots
	// cannot leak one graph per root (mirrors the sessions LRU bound).
	s.sessCache = session.NewCache(session.MaxSessions, session.SessionIdleEvict, s.evictSessionState)
	// register the built-in default agent so calls without an explicit
	// agent_id are governed (cwd-scoped) instead of raw. KERN_MCP_PERMISSIVE=1
	// remains the explicit opt-out that restores raw mode.
	governance.EnsureDefaultAgent()
	// the safety-budget ToolGateway is wired at construction only
	// when the operator opts in via at least one KERN_SAFETY_BUDGET_* env var.
	// Without opt-in the server stays budget-free (byte-for-byte legacy
	// behavior): per-task budgets are already enforced inside the loop, and a
	// process-wide cap would deny legitimate calls in long-lived sessions.
	// The gateway is budget-only (NewToolGateway(nil) — per-call firewalls are
	// built in newGovernor). WithToolGateway can wire, tune, or disable it
	// programmatically (nil = no-op).
	if safetyBudgetEnvSet() {
		s.gateway = governance.NewToolGateway(nil)
		s.budget = safetyBudgetFromEnv()
	}
	// Confinement is default-on: the KERN_MCP_ROOTS gate runs as the
	// pre-tool-use hook, so a tool call whose path-typed arguments resolve
	// outside the allowed roots is denied before any handler side effect runs.
	// KERN_MCP_NO_CONFINE=1 opts out entirely. With no KERN_MCP_ROOTS the gate
	// fails closed to the process cwd; KERN_MCP_PERMISSIVE=1 restores the old
	// allow-all loopback-client-trust behavior.
	if s.gate != nil {
		s.preTool = s.gate.Check
	}
	// Host model delegation for hosts that do not announce MCP sampling
	// (e.g. opencode): KERN_HOST_SAMPLER_CMD self-registers a command
	// sampler at startup — the auto LLM chain's host leg then shells the
	// command per generation. The command may be a non-interactive agent
	// invocation such as `opencode run`. KERN_HOST_SAMPLER_TIMEOUT (seconds)
	// bounds each call; KERN_HOST_SAMPLER_KEY namespaces the registration.
	if cmd := os.Getenv("KERN_HOST_SAMPLER_CMD"); strings.TrimSpace(cmd) != "" {
		timeout := time.Duration(0)
		if v := os.Getenv("KERN_HOST_SAMPLER_TIMEOUT"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
				timeout = time.Duration(secs) * time.Second
			}
		}
		key := os.Getenv("KERN_HOST_SAMPLER_KEY")
		if _, err := s.registerCommandSampler(cmd, timeout, key); err != nil {
			log.Printf("WARNING: failed to register host sampler (KERN_HOST_SAMPLER_CMD=%s, key %q): %v", cmd, key, err)
		}
	}
	return s
}

// NewServer returns a *Server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	s := newServerCore("stdio")
	s.in = in
	s.out = out
	return s
}

// NewServerForRoot returns a *Server wired to the given reader/writer whose
// confinement gate and workspace-root fallback are rooted at root — the
// project root the server serves — instead of the process working directory.
// KERN_MCP_ROOTS / mcp.roots config is deliberately NOT merged: a root-bound
// server confines to root ONLY, so a cross-App root list cannot widen one
// project's console (per-App isolation). The env keeps its widening
// semantics on the stdio NewServer path.
//
// Root-aware web-console tool servers MUST be built through this constructor:
// the stdio NewServer falls back to the process cwd, so an unrooted server
// backing every project's /v1/tools/{name} passthrough would run enterprise
// tool calls in the enterprise server's own cwd and let project A's console
// target project B's tree. The stdio reader is never consumed
// when the server is driven through CallToolGoverned.
func NewServerForRoot(in io.Reader, out io.Writer, root string) *Server {
	s := newServerCore("stdio")
	s.in = in
	s.out = out
	s.roots = workspaceRootsForRoot(root)
	// Re-root the confinement gate: same opt-out discipline as newServerCore
	// (KERN_MCP_NO_CONFINE=1 disables confinement entirely), but the
	// fail-closed default is root, never the process cwd.
	if os.Getenv("KERN_MCP_NO_CONFINE") != "1" {
		s.gate = mcpgate.NewGateForRoots([]string{root})
		s.preTool = s.gate.Check
	}
	return s
}

// confinementGate builds the KERN_MCP_ROOTS confinement gate, or nil when
// KERN_MCP_NO_CONFINE=1 opts out of confinement. A nil gate allows every
// call; a non-nil gate is enabled unless KERN_MCP_PERMISSIVE=1 opts out, and
// defaults its roots to the process cwd when KERN_MCP_ROOTS is unset.
func confinementGate() *mcpgate.Gate {
	if os.Getenv("KERN_MCP_NO_CONFINE") == "1" {
		return nil
	}
	return mcpgate.NewGateFromEnv()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	// Result/Error capture JSON-RPC RESPONSES from the client (top-level
	// fields). Requests never carry them; responses never carry a method.
	// They exist so server-initiated requests (host sampling) can correlate
	// the client's reply instead of misrouting it as a request.
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// isNotification reports whether the request is a JSON-RPC notification: it
// has no id, so the client expects no response. An EXPLICIT "id":null is
// tolerated as a notification only on the notifications/ namespace (some
// hosts serialize notifications that way); anywhere else a null id is an
// invalid request — see hasExplicitNullID (W9, 2026-10-26 black-box audit).
func (r rpcRequest) isNotification() bool {
	if len(r.ID) == 0 {
		return true
	}
	return string(r.ID) == "null" && strings.HasPrefix(r.Method, "notifications/")
}

// hasExplicitNullID reports whether the frame carried "id": null. JSON-RPC
// 2.0: a request id, when present, MUST NOT be null — only an absent id makes
// a notification.
func (r rpcRequest) hasExplicitNullID() bool {
	return string(r.ID) == "null"
}

// hasExplicitCursor reports whether the request params carry a non-null,
// non-empty "cursor" member (MCP list pagination). kern's catalog fits one
// page and never emits a cursor, so any client-supplied cursor is unknown.
func (r rpcRequest) hasExplicitCursor() bool {
	if len(r.Params) == 0 {
		return false
	}
	var p struct {
		Cursor *string `json:"cursor"`
	}
	if err := json.Unmarshal(r.Params, &p); err != nil {
		return false
	}
	return p.Cursor != nil && *p.Cursor != ""
}
