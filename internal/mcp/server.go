// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/doc"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/service"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
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

// NOTE: KERN_MCP_PHASE filters tool ADVERTISEMENT only (tools/list responses),
// not tool EXECUTION (tools/call). A client that knows a tool name can call it
// directly regardless of the phase setting. This is by design so kern_meta can
// route to unadvertised sub-tools. KERN_MCP_PHASE is NOT a security boundary —
// for real per-phase tool restriction, use the KERN_TOOLS allowlist.

// parseAllowlist reads the KERN_TOOLS allowlist from the environment. A
// comma-separated list restricts which tools the server exposes and executes;
// unset or empty means everything is allowed. The result is parsed once at
// server construction and cached on the Server, not re-read on every dispatch.
func parseAllowlist() []string {
	v := strings.TrimSpace(os.Getenv("KERN_TOOLS"))
	if v == "" {
		return nil
	}
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			// CLI subcommand aliases ("exec", "search") normalize to
			// canonical MCP tool names ("kern_exec", "kern_search") so
			// KERN_TOOLS behaves identically whether the operator writes
			// CLI-style or MCP-style names.
			out = append(out, governance.NormalizeToolName(n))
		}
	}
	return out
}

// highLevelOnly reports whether to expose only high-level tools (per the MCP
// spec) via KERN_MCP_HIGH_LEVEL_ONLY=1; otherwise all tools are registered.
func highLevelOnly() bool {
	return os.Getenv("KERN_MCP_HIGH_LEVEL_ONLY") == "1"
}

// singleTool reports whether to expose only the kern meta-tool via
// KERN_MCP_SINGLE_TOOL=1. Agents that find the tool catalog overwhelming
// can point their MCP config at this mode and interact with kern through the
// single natural-language `kern` entry point.
func singleTool() bool {
	return os.Getenv("KERN_MCP_SINGLE_TOOL") == "1"
}

// fullCatalog reports whether to advertise the full tool catalog via
// KERN_MCP_FULL=1. By default only the minimal defaultTools surface is
// advertised; this opts back in to the full catalog for power users
// and direct sub-tool callers. Phase-aware routing (KERN_MCP_PHASE) still
// filters the advertised list within the full catalog.
func fullCatalog() bool {
	return os.Getenv("KERN_MCP_FULL") == "1"
}

// mcpPhase returns the active agent phase from KERN_MCP_PHASE. An unset or
// invalid value returns "" which means no phase filtering: the whole tier
// surface (default/high-level/full) is advertised as before.
func mcpPhase() string {
	p := strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_PHASE")))
	if !validPhase(p) {
		return ""
	}
	return p
}

// validPhase reports whether p is one of the four agent phases.
func validPhase(p string) bool {
	switch p {
	case PhaseExplore, PhasePlan, PhaseEdit, PhaseVerify:
		return true
	}
	return false
}

// phaseToolAllowed reports whether a tool should be advertised for the active
// phase. Tools tagged "meta" or "cross" are always available; otherwise the
// tool's phase must equal the active phase. An empty phase allows everything.
func phaseToolAllowed(t Tool, phase string) bool {
	if phase == "" {
		return true
	}
	switch t.Phase {
	case PhaseMeta, PhaseCross:
		return true
	}
	return t.Phase == phase
}

// toolAllowed reports whether name passes the KERN_TOOLS allowlist. toolsList
// is the (already filtered or full) registered catalog; a nil/empty allowlist
// allows everything, otherwise name must appear in both the allowlist and
// the catalog. The allowlist slice is passed in (already cached on the
// Server) rather than re-read from the environment on every call.
func toolAllowed(toolsList []Tool, allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	inCatalog := false
	for i := range toolsList {
		if toolsList[i].Name == name {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return false
	}
	for i := range allowed {
		if allowed[i] == name {
			return true
		}
	}
	return false
}

// highLevelTools is the set of tools kept when KERN_MCP_HIGH_LEVEL_ONLY=1.
// It includes the 5 high-level orchestration tools (kern_analyze, kern_plan,
// kern_execute, kern_verify, kern_incident) plus a minimal set of essential
// primitives that high-level agents still need.
var highLevelTools = map[string]bool{
	"kern_analyze":              true,
	"kern_plan":                 true,
	"kern_execute":              true,
	"kern_verify":               true,
	"kern_incident":             true,
	"kern_what_if":              true,
	"kern_impact":               true,
	"kern_search":               true,
	"kern_context":              true,
	"kern_explore":              true,
	"kern_graph":                true,
	"kern_memory_add":           true,
	"kern_memory_list":          true,
	"kern_memory_recall":        true,
	"kern_memory":               true,
	"kern_review":               true,
	"kern_security":             true,
	"kern_validate":             true,
	"kern_repair_diagnostics":   true,
	"kern_refactor_transaction": true,
	"kern_run_build":            true,
	"kern_exec":                 true,
	"kern_sandbox":              true,
	"kern_commitmsg":            true,
	"kern_pack":                 true,
	"kern_project_map":          true,
	"kern_compact_file":         true,
	"kern_fit_context":          true,
	"kern_buddy":                true,
	"kern_usage_guide":          true,
	"kern_mask_pii":             true,
	"kern_optimize_prompt":      true,
	"kern_optimize_log":         true,
	"kern_doc_search":           true,
	"kern_doc_fetch":            true,
	"kern_doc_index":            true,
	"kern_context_budget":       true,
	"kern_swap":                 true,
	"kern_verify_output":        true,
	"kern_check_draft":          true,
	"kern_taint":                true,
	"kern_schema_validate":      true,
	"kern_stats":                true,
}

// defaultTools is the minimal surface advertised by default. The full
// full catalog is gated behind KERN_MCP_FULL=1, and phase-aware routing
// (KERN_MCP_PHASE) filters either surface down to the active phase's
// shortlist. kern_meta's NL router
// still reaches every sub-tool handler internally regardless of what is
// advertised, so no capability is lost — only the advertised surface
// shrinks. This implements the MCP spec's "high-level tools, not dozens
// of tiny low-value tools" guidance.
var defaultTools = map[string]bool{
	"kern_meta":              true, // NL router → all sub-tools
	"kern_explore":           true, // symbol source + callers/callees + blast radius
	"kern_impact":            true, // blast radius of a change
	"kern_review":            true, // token-optimised review context
	"kern_search":            true, // ranked symbol search
	"kern_context":           true, // minimal source slice
	"kern_optimize_prompt":   true, // compress prompts
	"kern_plan":              true, // implementation plan
	"kern_verify":            true, // unified verification
	"kern_run":               true, // orchestrate a whole task
	"kern_authorize_context": true, // authorized-context primitive (P0.1)
}

// filteredTools returns the registered tools minus any excluded by the
// KERN_TOOLS allowlist, intersected with the active agent phase from
// KERN_MCP_PHASE. By default only the minimal defaultTools surface is
// advertised; KERN_MCP_FULL=1 opts back in to the full catalog, the legacy
// KERN_MCP_HIGH_LEVEL_ONLY mode keeps the mid-size highLevelTools set for
// backward compat, and KERN_MCP_SINGLE_TOOL=1 collapses to kern_meta alone.
// Phase-aware routing (KERN_MCP_PHASE=explore|plan|edit|verify) keeps only
// the active phase's tools plus the always-on meta/cross tools; an unset or
// invalid phase advertises the whole tier surface. It lazily reads the env
// once per server lifetime and caches the result.
// schemaVersionFor returns the tool schema contract version this connection
// negotiated during initialize (P2-003), defaulting to the current catalog
// version when no handshake happened yet.
func (s *Server) schemaVersionFor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schemaVersion == "" {
		return SchemaVersionCurrent
	}
	return s.schemaVersion
}

func (s *Server) filteredTools() []Tool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	if s.filtered != nil {
		return s.filtered
	}
	if singleTool() {
		for _, t := range tools {
			if t.Name == "kern_meta" {
				s.filtered = []Tool{t}
				return s.filtered
			}
		}
	}
	allowed := s.allowlist
	// KERN_MCP_FULL=1 → advertise the full catalog (with KERN_TOOLS filter).
	if fullCatalog() {
		out := make([]Tool, 0, len(tools))
		for _, t := range tools {
			if len(allowed) > 0 {
				in := false
				for _, a := range allowed {
					if a == t.Name {
						in = true
						break
					}
				}
				if !in {
					continue
				}
			}
			if !phaseToolAllowed(t, mcpPhase()) {
				continue
			}
			out = append(out, t)
		}
		s.filtered = out
		return s.filtered
	}
	// KERN_MCP_HIGH_LEVEL_ONLY=1 → the legacy 38-tool middle set (deprecated;
	// prefer the default minimal set or KERN_MCP_FULL). Otherwise the NEW
	// DEFAULT: the minimal 11-tool defaultTools surface.
	var keep map[string]bool
	if highLevelOnly() {
		keep = highLevelTools
	} else {
		keep = defaultTools
	}
	out := make([]Tool, 0, len(keep))
	for _, t := range tools {
		if !keep[t.Name] {
			continue
		}
		if len(allowed) > 0 {
			in := false
			for _, a := range allowed {
				if a == t.Name {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		if !phaseToolAllowed(t, mcpPhase()) {
			continue
		}
		out = append(out, t)
	}
	s.filtered = out
	return s.filtered
}

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
	sessions  map[string]*project.Session
	// svc is the delivery-mechanism-independent service layer. Handlers
	// delegate core operations (index, graph, memory, governance, security)
	// to it instead of importing the internal engines directly, so the same
	// business logic serves CLI, MCP and web identically.
	svc *service.Services
	// platforms caches one application Platform per project root, keyed to
	// the exact index instance it was built from. High-level handlers used to
	// rebuild the whole Platform (call graph + 4 twin extractors, each a full
	// tree walk) on every tool call; the cache reuses it while the session
	// serves the same index instance and rebuilds only after a real index
	// rebuild (new instance pointer).
	platforms map[string]*platformEntry
	transport string // "stdio" (default) or "http"
	// schemaVersion is the tool schema contract version (P2-003) negotiated
	// during initialize: the client's requested version when supported,
	// else SchemaVersionCurrent. Empty until the first initialize; getters
	// fall back to SchemaVersionCurrent so tools/list is still correct when
	// a client skips the handshake.
	schemaVersion string
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
	gate *Gate
	// commits caches the short HEAD commit per project root so git is spawned
	// at most once per root per server lifetime.
	commits map[string]string
	// indexOnce defers background index preloading until the first MCP
	// request (initialize or tools/call) instead of server startup, so
	// setup is instant and the index cost is paid while the user waits.
	indexOnce sync.Once
	// indexedRoots records which project roots have completed at least one
	// index build this process, so the "first build in progress" notice is
	// printed once per root instead of on every stale rebuild.
	indexedRoots sync.Map
	// audit records every executed (and pre-dispatch-rejected) MCP tool
	// call into the project's tamper-evident audit chain; auditMu
	// guards toolAudit's lazy initialization.
	audit   *governance.AuditLog
	auditMu sync.Mutex
	// preTool is an optional hook invoked before every tools/call execution.
	// It receives the tool name and its arguments and returns nil to allow the
	// call or an error to deny it (denial surfaces as a tool error response,
	// isError=true, no side effects). A nil hook preserves the default
	// behavior exactly — callers that never set it see zero change.
	preTool func(name string, args map[string]any) error
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
// Configurable via KERN_MCP_CONCURRENCY (default 32, minimum 8).
func defaultConcurrency() int {
	if v := os.Getenv("KERN_MCP_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 8 {
			return n
		}
	}
	return 32
}

// newServerCore constructs the transport-independent core of a Server: the
// shared service layer, the KERN_TOOLS allowlist, the sampling slots, the
// confinement gate wiring, the opt-in safety-budget gateway, and
// host-sampler registration. Both transports (stdio NewServer and HTTP
// ServeHTTPContextWithTLS) must build through it — the HTTP path previously
// hand-rolled its own struct literal, silently skipping
// svc/allowlist/samplerKey/samplingSlots, which made svc-backed tools
// (memory, security) panic and KERN_TOOLS a no-op over HTTP.
func newServerCore(transport string) *Server {
	// Inject the live tool catalog into the diff-gate drift checks explicitly
	// at server construction —
	// never via init(). cli (which cannot import mcp) reads it through
	// diffgate.ToolInfos() when building the diff-gate check list; a binary
	// that never constructs a server (e.g. a pure `kern diff-gate` CLI run)
	// is wired in cmd/kern main.
	catalog.WithDiffgateTools()
	s := &Server{sem: make(chan struct{}, defaultConcurrency()), locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}, sessions: map[string]*project.Session{}, svc: service.New(), transport: transport, roots: defaultWorkspaceRoots(), gate: confinementGate(), commits: map[string]string{}, allowlist: parseAllowlist(), watchStop: make(chan struct{}), watchDone: make(chan struct{}), samplerKey: samplerKeyFor(), samplingSlots: map[string]func(){}}
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
		_, _ = s.registerCommandSampler(cmd, timeout, key)
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

// confinementGate builds the KERN_MCP_ROOTS confinement gate, or nil when
// KERN_MCP_NO_CONFINE=1 opts out of confinement. A nil gate allows every
// call; a non-nil gate is enabled unless KERN_MCP_PERMISSIVE=1 opts out, and
// defaults its roots to the process cwd when KERN_MCP_ROOTS is unset.
func confinementGate() *Gate {
	if os.Getenv("KERN_MCP_NO_CONFINE") == "1" {
		return nil
	}
	return NewGateFromEnv()
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
// has no id, so the client expects no response.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.out.Write(data); err != nil {
		return err
	}
	_, err = s.out.Write([]byte{'\n'})
	return err
}

// progress sends a notifications/progress message for a tool call. Notifications
// are only pushed on transports that can deliver them (stdio); HTTP answers
// each request with a single response body and has no push channel (SSE is
// not supported). ctx guards against emitting progress after the request was
// cancelled or answered.
func (s *Server) progress(ctx context.Context, id string, tool string, pct int, msg string) {
	if s.transport != "stdio" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	n := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]any{
			"progressToken": id,
			"progress":      pct,
			"total":         100,
			"message":       msg,
		},
	}
	_ = s.write(n)
}

// startProgress emits an initial 0% notification for a slow tool and returns a
// stop func that emits 100% and halts the background ticker. stop blocks until
// the ticker goroutine has exited, so a stale "still running" emission can
// never arrive after "finished"; every emission is gated on ctx.Err() and the
// writer mutex, so a progress message can never arrive after the final
// response.
// slowTools are the tools whose dispatch can take seconds to minutes; they
// emit MCP progress notifications so agents see liveness instead of
// silence during multi-second tool calls. The "slow" flag lives on the
// per-tool catalog table (catalog.Tool.Slow in internal/mcp/catalog/tools.go)
// — slowness is declared next to each tool definition (index/build/scan,
// sandbox, heal, validate, refactor, repair), not in a drifting side map —
// and this set is derived from it. Fast lookups (search, explore, graph,
// context) emit no progress and stay silent. Tests may replace the whole map
// to exercise the central runTool wrap. LLM-class tools (kern_analyze/
// kern_plan) are deliberately NOT slow-flagged: their responses are captured
// byte-exact by cross-interface tests, and the LLM legs have their own
// latency story.
var slowTools = func() map[string]bool {
	m := make(map[string]bool, 16)
	for _, t := range catalog.All {
		if t.Slow {
			m[t.Name] = true
		}
	}
	return m
}()

func (s *Server) startProgress(ctx context.Context, id, tool string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	s.progress(ctx, id, tool, 0, tool+" running")
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.progress(ctx, id, tool, -1, tool+" still running")
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
		s.progress(ctx, id, tool, 100, tool+" finished")
	}
}

// cancelAll cancels every in-flight tool call and releases every lock held by
// this server. It is invoked on graceful shutdown (SIGINT/SIGTERM) so slow
// tools stop promptly and the workspace is left unlocked. The inflight map is
// deliberately NOT cleared here: each running tool goroutine removes its own
// entry (unregisterInflight) once it finishes, so Inflight() keeps reporting
// still-running tools and the shutdown drain can wait for them to exit.
func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, cancel := range s.inflight {
		cancel()
	}
	var held []*lock.Lock
	for _, lk := range s.locks {
		held = append(held, lk)
	}
	s.locks = map[string]*lock.Lock{}
	s.mu.Unlock()
	for _, lk := range held {
		_ = lk.Release()
	}
}

// CancelAll aborts in-flight tools and releases held locks. Safe to call from
// a signal handler or after Serve returns.
func (s *Server) CancelAll() { s.cancelAll() }

// Inflight returns the number of tool calls currently registered as
// in-flight. Graceful shutdown polls it after CancelAll so it can wait for
// cancelled tools to drain their responses before exiting.
func (s *Server) Inflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// registerInflight stores the cancel func for a request id so $/cancelRequest
// and graceful shutdown can abort it.
func (s *Server) registerInflight(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]context.CancelFunc{}
	}
	s.inflight[id] = cancel
	s.mu.Unlock()
}

// unregisterInflight removes the cancel func once the tool call has finished.
func (s *Server) unregisterInflight(id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

// Serve runs until the stream ends.
// preloadIndexes builds and caches each workspace root's index in the
// background so a later tool call reuses it instead of blocking on a cold
// build. It is triggered lazily on the first MCP request (see indexOnce) and
// runs in a goroutine so setup never blocks on it. Skipped for filesystem
// roots and when KERN_PRELOAD=0 (test/CI guard).
func (s *Server) preloadIndexes() {
	if os.Getenv("KERN_PRELOAD") == "0" {
		return
	}
	for _, r := range s.workspaceRoots() {
		if isFilesystemRoot(r) {
			continue
		}
		go func(root string) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: panic recovered: %v\n", root, r)
				}
			}()
			_, err := s.sessionFor(root).Index()
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: %v\n", root, err)
				return
			}
			// Record the build so a later graph tool call that reuses this
			// warm index does not print the "first build in progress" notice.
			s.indexedRoots.Store(root, true)
		}(r)
	}
}

// isFilesystemRoot reports whether p is a filesystem root ("/" on Unix, or a
// drive root on Windows) that must never be treated as a project to index.
func isFilesystemRoot(p string) bool {
	vol := filepath.VolumeName(p)
	return filepath.Clean(p) == filepath.Clean(vol+string(filepath.Separator))
}

// workspaceRoots returns the roots field, falling back to the default when
// unset (defensive; NewServer always initializes it).
func (s *Server) workspaceRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.roots) == 0 {
		return defaultWorkspaceRoots()
	}
	out := append([]string(nil), s.roots...)
	return out
}

// Serve runs the MCP server loop until EOF or a write error, draining
// in-flight tool calls before returning.
func (s *Server) Serve() error {
	if s.sem == nil {
		s.sem = make(chan struct{}, defaultConcurrency())
	}
	var wg sync.WaitGroup
	// EOF or a write error ends the server: drain in-flight tool calls
	// first (a tool call may be the one queuing a background index save),
	// then Close() so the sessions' background index saves (saveWG) and
	// file watchers are drained before Serve returns. Without this, a
	// caller that tears down its workspace right after Serve returns —
	// e.g. a test removing its t.TempDir — races the late persister
	// writes ("unlinkat .kern: directory not empty"). Close is
	// idempotent, so the explicit Close() in the CLI shutdown paths is
	// unaffected.
	defer func() {
		wg.Wait()
		s.Close()
	}()
	// newScanner rebuilds the stdio line scanner. A scanner cannot be reused
	// after it hits bufio.ErrTooLong, so it is recreated from the raw reader.
	newScanner := func() *bufio.Scanner {
		sc := bufio.NewScanner(s.in)
		sc.Buffer(make([]byte, 64<<20), 64<<20)
		return sc
	}
	for {
		sc := newScanner()
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var req rpcRequest
			if err := json.Unmarshal(line, &req); err != nil {
				// A malformed line is a protocol error, not a silent drop.
				if err := s.write(errorResponse(nil, -32700, "parse error: "+err.Error())); err != nil {
					return err
				}
				continue
			}
			if req.Method == "" && rawPresent(req.Result) || req.Method == "" && rawPresent(req.Error) {
				// A response to a server-initiated request (host sampling):
				// deliver it to the waiting sampler, never dispatch it as a
				// request.
				if s.deliverSamplingReply(req) {
					continue
				}
			}
			if req.Method == "tools/call" {
				// Run tools concurrently so a slow tool (build, heal, LLM
				// optimize) can never freeze the stdio server: $/cancelRequest,
				// progress and other tool calls keep being served while it runs.
				// Response writes are serialized by s.write's mutex, and the
				// request is captured by value so the loop can move on.
				// Concurrency is bounded by s.sem so a burst of tool calls cannot
				// spawn unbounded goroutines that each build a full project index.
				wg.Add(1)
				s.sem <- struct{}{}
				go func(req rpcRequest) {
					defer wg.Done()
					defer func() { <-s.sem }()
					if resp := s.safeDispatch(req); resp != nil {
						if err := s.write(resp); err != nil {
							fmt.Fprintf(os.Stderr, "kern-mcp: write response: %v\n", err)
						}
					}
				}(req)
				continue
			}
			if resp := s.safeDispatch(req); resp != nil {
				if err := s.write(resp); err != nil {
					return err
				}
			}
		}
		// A single oversized message (>64MB) must not kill the server: the
		// scanner hit bufio.ErrTooLong, so skip the oversized token (in 64MB
		// chunks until its newline passes), recreate the scanner and keep
		// serving instead of terminating on the error.
		if sc.Err() == bufio.ErrTooLong {
			fmt.Fprintf(os.Stderr, "kern-mcp: skipping oversized input line (> %d bytes); continuing\n", 64<<20)
			continue
		}
		return sc.Err()
	}
}

// safeDispatch computes a request's response, converting any panic into an
// internal-error response instead of crashing the server.
func (s *Server) safeDispatch(req rpcRequest) (r any) {
	defer func() {
		if rec := recover(); rec != nil {
			r = errorResponse(req.ID, -32603, fmt.Sprintf("internal error: %v", rec))
			fmt.Fprintf(os.Stderr, "kern-mcp: panic serving %s: %v\n%s\n", req.Method, rec, debug.Stack())
		}
	}()
	return s.dispatch(req)
}

// Close stops all background file watchers associated with this server's
// sessions and halts the implicit background index watch, draining an
// in-flight rebuild for up to 5s. It is safe to call multiple times.
func (s *Server) Close() {
	s.mu.Lock()
	for _, sess := range s.sessions {
		sess.Close()
	}
	s.mu.Unlock()
	// Unregister every host sampler slot: no LLM call may delegate to a host
	// that is shutting down (registrations are effects — dispose them).
	s.samplingMu.Lock()
	for _, d := range s.samplingSlots {
		d()
	}
	s.samplingSlots = map[string]func(){}
	s.samplingHost = false
	s.samplingMu.Unlock()
	// Stop the background index watch: no rebuild may start after this point,
	// and an in-flight rebuild is drained briefly (see stopWatch).
	s.stopWatch()
}

// dispatch computes the JSON-RPC response for a request. A nil return means
// the request needs no response (e.g. a notification). The response is a
// transport-neutral object so both stdio and HTTP can send it.
func (s *Server) dispatch(req rpcRequest) any {
	// KERN_MCP_ROOTS confinement gate (memory-#31): every tools/call whose
	// path-typed arguments (root, dir, or any key containing "path") resolve
	// outside the allowed roots is rejected here, before any handler runs.
	// The rejection uses the same result shape a handler error produces — a
	// tool result with isError=true — so the client sees a clean tool error
	// rather than a panic or a JSON-RPC error. A nil or disabled gate is a
	// no-op, keeping the default (loopback-client trust) behavior identical.
	if s.gate != nil && s.gate.enabled && req.Method == "tools/call" {
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			if err := s.gate.Check(p.Name, p.Arguments); err != nil {
				denied := "pre-tool-use denied: " + err.Error()
				result := map[string]any{
					"content": []any{map[string]any{"type": "text", "text": denied}},
					"isError": true,
				}
				attachTokenMetadata(result, p.Name, p.Arguments, denied)
				return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
			}
		}
	}
	// $/cancelRequest is a notification per JSON-RPC (no response expected),
	// but must still be processed: dispatch returns nil for notifications
	// below, so it is handled here before the short-circuit.
	if req.Method == "$/cancelRequest" {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(req.Params, &p)
		key := idKey(p.ID)
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
		// Kick off background indexing on the first connection so setup stays
		// instant: graph tools block until the build finishes, while tools
		// that don't need the index serve immediately. indexOnce guards the
		// trigger so it fires exactly once per server lifetime.
		s.indexOnce.Do(func() { go s.preloadIndexes() })
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
			Capabilities    struct {
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
		// client actually speaks.
		schemaVersion := negotiateSchemaVersion(initParams.SchemaVersion)
		s.mu.Lock()
		s.schemaVersion = schemaVersion
		s.mu.Unlock()
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": version,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
				"schemaVersion":   schemaVersion,
				"instructions":    instructions,
			},
		}
	case "notifications/initialized":
		return nil
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": s.filteredTools(), "schemaVersion": s.schemaVersionFor()}}
	case "tools/call":
		return s.toolCallResponse(req.ID, req.Params)
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
// outside the confined workspace.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return withinRoot(cwd, p)
	}
	return withinRoot(root, p)
}

func (s *Server) runTool(ctx context.Context, id string, name string, args map[string]any) (out string, runErr error) {
	// Record every incoming tool call and its duration; the defer covers all
	// return paths, and runErr is non-nil exactly when dispatch returned an error.
	origName := name // precheckTool may remap the name; audit the executed one
	metrics.Default().RecordRequest()
	start := time.Now()
	fromCache := false
	defer func() {
		// D1: cache-hit durations must not pollute the RecordToolCall latency
		// percentiles — hits are counted under RecordCacheHit instead (F4).
		if !fromCache {
			metrics.Default().RecordToolCall(time.Since(start))
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
			s.auditToolCall(name, args, runErr, true, fromCache)
		} else {
			s.auditToolCall(origName, args, runErr, false, fromCache)
		}
	}()
	name, err := s.precheckTool(name, args)
	if err != nil {
		return "", err
	}
	// D6: enforce the string-argument coercion contract (accept string/number/
	// bool; reject null/object/array) before the handler runs, so a wrong-typed
	// argument surfaces as a clear isError instead of a silent mis-coercion.
	if err := validateStringArgs(name, args); err != nil {
		return "", err
	}
	// D1 — tool-response cache: check INSIDE runTool, after precheckTool and
	// validateStringArgs succeed and before dispatchTool (F4), so a cache hit
	// never skips the KERN_TOOLS allowlist, root validation, the 	// safety budget, metrics or the audit chain. Only Cacheable (explicit
	// opt-in allowlist, F1) tools participate; KERN_MCP_CACHE=0 and per-call
	// no_cache=1 bypass. The effective flag is call-level (cacheableForCall):
	// kern_meta participates only when the sub-tool it routes to is itself
	// cacheable (R1), and semantic calls never participate (R3). Lookup and
	// store below use the same gate, so both paths are covered.
	if cacheableForCall(name, args) && cacheEnabled() && !noCacheArg(args) {
		if text, hit := s.cacheLookup(ctx, name, args); hit {
			fromCache = true
			metrics.Default().RecordCacheHit()
			return text, nil
		}
		metrics.Default().RecordCacheMiss()
	}
	// slow tools emit MCP progress notifications (0% start, 5s
	// keep-alive, 100% stop) so agents see liveness instead of silence during
	// multi-second tool calls. Centralized here (sandbox/heal/run_build
	// previously started progress in their handlers; the central wrap
	// supersedes those). Cache hits return before this point, so they never
	// emit progress.
	if slowTools[name] {
		stop := s.startProgress(ctx, id, name)
		defer stop()
	}
	text, err := s.dispatchTool(ctx, id, name, args)
	// D1: store only successful (non-error) results; the response text is
	// the raw pre-sandbox output (max_output is applied at serve time).
	if err == nil && cacheableForCall(name, args) && cacheEnabled() && !noCacheArg(args) {
		s.cacheStore(ctx, name, args, text)
	}
	return text, err
}

// precheckTool validates a tool name against the KERN_TOOLS allowlist and the
// root argument before dispatch, resolving blocked tools to their
// policy-approved fallback. It returns the (possibly fallback) tool name to
// dispatch, or an error when the tool is not allowed and no allowed
// alternative exists, or when the root argument fails validation.
func (s *Server) precheckTool(name string, args map[string]any) (string, error) {
	// Validates against the full registered catalog by design — phase
	// filtering (KERN_MCP_PHASE) only affects advertisement, never execution.
	// Resolve against the FULL registered catalog, not the advertised
	// (filtered) set: kern_meta's NL router reaches every sub-tool handler
	// internally even when the sub-tool is not advertised, and an explicit
	// KERN_TOOLS allowlist still gates execution here.
	if !toolAllowed(tools, s.allowlist, name) {
		// Tool fallback: when a tool is blocked by the KERN_TOOLS
		// allowlist, route to its policy-approved alternative if one exists and
		// IS allowed, so a restricted deployment still gets an equivalent
		// result instead of a hard failure. Fail closed when no allowed
		// alternative exists.
		if alt := app.FallbackFor(name); alt != "" {
			if toolAllowed(tools, s.allowlist, alt) {
				name = alt
			}
		}
		// Fail closed only when no allowed alternative exists.
		if !toolAllowed(tools, s.allowlist, name) {
			return "", fmt.Errorf("tool %q is not allowed (KERN_TOOLS allowlist)", name)
		}
	}
	if err := s.checkRootArg(args); err != nil {
		return "", err
	}
	if root := argString(args, "root"); root != "" {
		if err := validateRoot(root); err != nil {
			return "", err
		}
	}
	// track every tool call against the safety budget through the
	// ToolGateway. A nil gateway or nil budget is a safe no-op (back-compat).
	// The budget gate runs after allowlist/root validation so blocked calls
	// never consume budget; when the budget is already exceeded the call is
	// denied with a structured error before any handler side effect runs.
	// Evaluate's boundary is empty (the tool name is the resource) and its
	// firewall is nil (per-call firewalls live in newGovernor), so only the
	// budget dimension is enforced here.
	if s.gateway != nil && s.budget != nil {
		s.budgetMu.Lock()
		defer s.budgetMu.Unlock()
		agentID := argString(args, "agent_id")
		if agentID == "" {
			agentID = governance.DefaultAgentID
		}
		if _, _, _, gerr := s.gateway.Evaluate(agentID, argString(args, "task"), name, "call", domain.TaskBoundary{}, s.budget); gerr != nil {
			// Surface the budget reason verbatim in the structured denial.
			if _, reason := s.budget.Exceeded(); reason != "" {
				return "", fmt.Errorf("safety budget exceeded: %s — tool call denied", reason)
			}
			return "", fmt.Errorf("tool call denied by safety gateway: %s", gerr)
		}
		s.budget.TrackToolCall()
	}
	return name, nil
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

// truncateMCP truncates s to n bytes with a visible continuation marker.
func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// sessionFor returns the project session for root, creating and caching one
// per root so index state and stats identity are shared across tool calls.
func (s *Server) sessionFor(root string) *project.Session {
	root = resolveRoot(root)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*project.Session{}
	}
	sess, ok := s.sessions[root]
	if !ok {
		sess = project.New(root, "")
		s.sessions[root] = sess
	}
	return sess
}

// resolveRoot cleans a tool root argument to an absolute path and requires it
// to be an existing directory. An empty root falls back to the current working
// directory. This guards every index-using tool against traversal-style root
// values and turns confusing downstream index errors into clear ones.
func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	return root
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// If either resolution fails (e.g. the file does not exist yet), fall back
	// to the lexical Clean+Rel check below.
	if rRoot, rerr := filepath.EvalSymlinks(root); rerr == nil {
		if rAbs, aerr := filepath.EvalSymlinks(abs); aerr == nil {
			rel, err := filepath.Rel(rRoot, rAbs)
			if err != nil {
				return "", fmt.Errorf("resolve %q: %w", file, err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return "", fmt.Errorf("path %s escapes project root %s", abs, root)
			}
			return abs, nil
		}
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}

// validateRoot rejects a tool root that exists on disk but is not a directory
// (e.g. a file). A nonexistent root is allowed because several tools create the
// target directory before indexing it. The filesystem root ("/") is also
// rejected: it is never a project to index. It returns "" on success so it can
// be called inline as `if msg := validateRoot(root); msg != "" { return "", msg }`.
func validateRoot(root string) error {
	clean := filepath.Clean(resolveRoot(root))
	if isFilesystemRoot(clean) {
		return fmt.Errorf("root %q is a filesystem root and cannot be treated as a project", clean)
	}
	if st, err := os.Stat(clean); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("root %q exists but is not a directory", clean)
		}
	}
	return nil
}

// indexScope carries the symbol index loaded during one tool call so provenance
// can be stamped onto that same call's response. It lives on the per-request
// context instead of the Server struct, so concurrent tool calls never share a
// mutable lastIndex and read each other's provenance.
type indexScope struct {
	ix   *index.Index
	prov *Provenance // structured evidence stamped by retrieval handlers
}
type indexScopeKey struct{}

// loadIndex returns the session's symbol index, reused while fresh and rebuilt
// when stale or missing (see project.Session.Index). The returned index is
// recorded on the per-call scope so the tool response can be stamped with
// provenance.
func (s *Server) loadIndex(ctx context.Context, root string) (*index.Index, error) {
	// First time this root is indexed in-process (including while a lazy
	// background preload is still running): tell the user the wait is the
	// initial build, not a hang. Subsequent stale rebuilds stay silent.
	if _, built := s.indexedRoots.Load(root); !built {
		if root == "" {
			fmt.Fprintf(os.Stderr, "kern-mcp: first index build in progress, tool call waiting...\n")
		} else {
			fmt.Fprintf(os.Stderr, "kern-mcp: first index build for %s in progress, tool call waiting...\n", root)
		}
	}
	ix, err := s.sessionFor(root).Index()
	if err != nil {
		return nil, err
	}
	s.indexedRoots.Store(root, true)
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		scope.ix = ix
	}
	return ix, nil
}

// platformEntry ties a cached Platform to the exact index instance it was
// built from. The Platform owns the call graph and the twin-merged knowledge
// graph; constructing it runs intelligence.FromIndex plus four twin
// extractors, each a full filesystem walk. Reusing it while the session
// serves the same index instance turns ~5 tree walks per high-level tool
// call into zero.
type platformEntry struct {
	ix *index.Index
	p  *app.Platform
}

// platformFor returns the shared application Platform for root, caching it
// per root keyed by the index instance it was built from. While
// project.Session serves the same *index.Index pointer (fresh index, 1s
// staleness cooldown), the cached Platform is returned; a real index rebuild
// allocates a new instance, so the next call rebuilds the Platform exactly
// once. The Platform and its graph are treated as read-only after
// construction (same contract as web.App), so sharing across tool calls is
// safe. Handlers that previously called loadIndex + app.NewWithIndex per
// call should use this instead.
func (s *Server) platformFor(ctx context.Context, root string) (*app.Platform, error) {
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	s.mu.Lock()
	if s.platforms == nil {
		s.platforms = map[string]*platformEntry{}
	}
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p := e.p
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	p, err := app.NewWithIndex(root, ix)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Another caller may have populated the cache while we built; prefer the
	// existing entry when it matches the same index instance (identical
	// content, saves the duplicate graph).
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p = e.p
	} else {
		s.platforms[root] = &platformEntry{ix: ix, p: p}
	}
	s.mu.Unlock()
	return p, nil
}

// commit returns the short HEAD commit of root, cached per root. Git is
// optional: a non-repo root or missing git yields "" (omitted from the stamp).
func (s *Server) commit(root string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.commits[root]; ok {
		return c
	}
	c := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		c = strings.TrimSpace(string(out))
	}
	s.commits[root] = c
	return c
}

// splitShellLine tokenizes a command line into argv, honoring single and
// double quotes so the documented `sh -c 'cmd ...'` (and Windows `cmd /c "..."`)
// form is preserved as a single argument instead of being split by whitespace.
func splitShellLine(line string) []string {
	var (
		out      []string
		cur      strings.Builder
		inSingle bool
		inDouble bool
		started  bool
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
			started = true
		case c == '"':
			inDouble = true
			started = true
		case c == ' ' || c == '\t':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func renderOptimize(label string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		label, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
}

// clipForMarker shortens a matched-input preview so a multi-megabyte log match
// never floods the "served from semantic cache" marker.
func clipForMarker(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sanitizeDocName(name string) (string, error) {
	return doc.SanitizeDocName(name)
}

func renderStats(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	sum, err := rec.Summarize(days, session)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("operations=%d before=%d after=%d saved=%d (%.1f%%) cost_saved=$%.4f",
		sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved), nil
}
