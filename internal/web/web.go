// Package web is a small, read-only, stdlib-only HTTP console that serves the
// project's digital-twin data as JSON plus a minimal server-rendered HTML
// dashboard. Routing uses net/http's ServeMux, the dashboard uses html/template
// with a single embedded template, and all payloads use encoding/json — no
// external dependencies are required. Read-only endpoints are fail-closed:
// build or validation errors surface as 500 JSON rather than panicking.
//
// A small set of write endpoints (approve/reject approvals, record incidents)
// is exposed for a loopback/local console, where the loopback client is the
// trusted principal. These are not an authentication boundary and must not be
// exposed beyond the loopback.
package web

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	kerncontext "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// App holds the project root and the derived read-only state for the console.
// It delegates routing to an embedded http.ServeMux via ServeHTTP.
type App struct {
	root       string
	mux        *http.ServeMux
	ix         *index.Index
	graph      intelligence.Graph
	inc        *incident.Engine     // prebuilt incident engine (shares a.graph)
	ver        *verification.Engine // prebuilt verification engine (shares a.ix)
	archIndex  *index.Index         // shared index for architecture validation
	memories   *memory.MemoryStore
	inter      *incident.Store
	firewall   *governance.Firewall
	approvals  *governance.ApprovalWorkflow
	dashboardT *template.Template
	ctx        *kerncontext.Engine // context engine for analyze/plan
	bus        *eventbus.Bus       // publishes incident/approval events
	tasks      *agent.Registry     // agent/task registry for /v1/tasks lookup

	// archTTL is how long a validated architecture report is cached before the
	// next /api/architecture, /api/overview or "/" request re-runs
	// architecture.ValidateProject (which re-indexes the whole repo via
	// index.Build). Caching at ~5s means a polled dashboard re-indexes at most
	// once every 5s instead of once per request. Trade-off: a repository edited
	// between polls may be reflected up to archTTL late; 5s is short enough that
	// an interactive dashboard never shows a visibly stale report while
	// eliminating the per-request re-index hot path.
	archTTL time.Duration
	archMu  sync.Mutex
	archAt  time.Time
	archRep *architectureData

	// graphMu guards the ix/graph swap performed by freshGraph so concurrent
	// requests that detect staleness share a single rebuild instead of racing
	// to re-index simultaneously. graphVer is bumped on every rebuild so
	// callers/logs can observe that a refresh happened.
	graphMu  sync.RWMutex
	graphVer int
}

// New builds the digital-twin state for root and returns a ready-to-serve App.
// It never panics: any build error is wrapped and returned to the caller.
func New(root string) (*App, error) {
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	g := intelligence.FromIndex(ix)

	a := &App{
		root:      root,
		ix:        ix,
		graph:     g,
		memories:  memory.NewMemoryStore(root),
		inter:     incident.NewStore(root),
		firewall:  governance.NewFirewall(),
		approvals: governance.NewApprovalWorkflow(),
		tasks:     agent.NewRegistry(),
		archTTL:   5 * time.Second,
	}
	// Back the /v1/tasks registry with a persisted task store so submitted
	// tasks survive across server restarts and handleV1Task can serve a real,
	// non-empty task registry (returning 404 only when a task is genuinely
	// unknown).
	a.tasks.SetTaskStore(agent.NewTaskStore(root))
	a.ctx = kerncontext.NewEngine(root, &a.graph, a.memories, a.firewall)
	// Wire the production-intelligence source and architecture boundary provider
	// into the context engine so shipped runs populate ContextPacket.RuntimeEvidence
	// (when a local snapshot exists) and surface boundary rules in
	// ArchitectureRules. Both are nil-safe: with no snapshot / no boundaries the
	// packet stays empty rather than fabricated.
	a.ctx.WithRuntimeSource(runtimeSource(root))
	a.ctx.WithBoundaryProvider(boundaryProvider(root))
	a.bus = eventbus.New()
	// Prebuild the per-request engines ONCE at startup and share the already
	// built index/graph so handlers never re-index the repo per request (this
	// was the #1 bottleneck: /v1/incidents/investigate and /v1/verify each
	// re-ran index.Build). The engines store only read-only references to
	// a.ix / a.graph and are safe for concurrent handler use.
	incEng, err := incident.NewEngineWithGraph(root, &a.graph, runtime.NewStore(), a.memories, a.firewall)
	if err != nil {
		return nil, err
	}
	a.inc = incEng
	a.ver = verification.NewEngineWithIndex(root, a.ix)
	a.archIndex = a.ix
	tmpl, err := parseDashboardTemplate()
	if err != nil {
		return nil, err
	}
	a.dashboardT = tmpl

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/overview", a.handleOverview)
	mux.HandleFunc("/api/graph", a.handleGraph)
	mux.HandleFunc("/api/memory", a.handleMemory)
	mux.HandleFunc("/api/incidents", a.handleIncidents)
	mux.HandleFunc("/api/architecture", a.handleArchitecture)
	mux.HandleFunc("/api/governance", a.handleGovernance)
	mux.HandleFunc("/api/governance/metrics", a.handleGovernanceMetrics)
	mux.HandleFunc("/api/performance", a.handlePerformance)
	mux.HandleFunc("/api/approvals/pending", a.handleApprovalsPending)
	mux.HandleFunc("/api/approvals/approve", a.handleApprovalApprove)
	mux.HandleFunc("/api/approvals/reject", a.handleApprovalReject)
	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/v1/analyze", a.handleV1Analyze)
	mux.HandleFunc("/v1/plan", a.handleV1Plan)
	mux.HandleFunc("/v1/what-if", a.handleV1WhatIf)
	mux.HandleFunc("/v1/impact", a.handleV1Impact)
	mux.HandleFunc("/v1/verify", a.handleV1Verify)
	mux.HandleFunc("/v1/memory", a.handleV1Memory)
	mux.HandleFunc("/v1/graph/", a.handleV1Graph)
	mux.HandleFunc("/v1/context", a.handleV1Context)
	mux.HandleFunc("/v1/risk", a.handleV1Risk)
	mux.HandleFunc("/v1/agents", a.handleV1Agents)
	mux.HandleFunc("/v1/loop", a.handleV1Loop)
	mux.HandleFunc("/v1/incidents/investigate", a.handleV1IncidentInvestigate)
	mux.HandleFunc("/v1/tasks", a.handleV1TaskSubmit)
	mux.HandleFunc("/v1/tasks/", a.handleV1Task)
	a.mux = mux
	return a, nil
}

// ServeHTTP routes requests through the registered mux. For request methods
// that carry a body (POST/PUT/PATCH), it caps the body at 1MB via
// http.MaxBytesReader so a client cannot exhaust memory by streaming an
// unbounded request body to the JSON decoders.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	a.mux.ServeHTTP(w, r)
}

// freshGraph returns the current knowledge graph and index, rebuilding both if
// the project has changed since the last build. Staleness is detected via the
// index package's Stale() (file set + content-hash check). The rebuild runs at
// most once per stale project, not once per request: a fast mtime/count gate
// inside Stale() short-circuits most calls. Concurrent requests that all observe
// staleness serialize on graphMu so only one of them performs the rebuild; the
// rest wait and return the freshly swapped state.
func (a *App) freshGraph() (*intelligence.Graph, *index.Index) {
	a.graphMu.RLock()
	if !a.ix.Stale() {
		g, ix := &a.graph, a.ix
		a.graphMu.RUnlock()
		return g, ix
	}
	a.graphMu.RUnlock()

	a.graphMu.Lock()
	defer a.graphMu.Unlock()
	// Re-check under the write lock: another request may have already rebuilt
	// while we were waiting for the write lock.
	if !a.ix.Stale() {
		return &a.graph, a.ix
	}
	if nix, err := index.Build(a.root); err == nil {
		a.ix = nix
		a.graph = intelligence.FromIndex(nix)
		a.archIndex = nix
		a.graphVer++
	}
	return &a.graph, a.ix
}

// runtimeSource returns the local production-intelligence source for root when
// a snapshot exists at .kern/runtime.json; otherwise it returns nil (nil-safe),
// so the context engine leaves RuntimeEvidence empty rather than fabricating it.
func runtimeSource(root string) runtime.Source {
	st, err := runtime.LoadJSON(filepath.Join(root, ".kern", "runtime.json"))
	if err != nil {
		return nil
	}
	return st
}

// boundaryProvider returns a function that surfaces .kern/boundaries.json rules
// as governance policies, letting the context engine emit boundary rules in
// ArchitectureRules when a change crosses them. A missing/invalid file yields
// an empty rule set (never an error), so the engine stays nil-safe.
func boundaryProvider(root string) func() []domain.Policy {
	return func() []domain.Policy {
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return nil
		}
		out := make([]domain.Policy, 0, len(b.Rules))
		for _, r := range b.Rules {
			out = append(out, domain.FromGuardRule(r))
		}
		return out
	}
}

// Bus returns the App's shared event bus so callers (e.g. kern-server) can
// subscribe and fan events out to webhooks or an audit trail.
func (a *App) Bus() *eventbus.Bus { return a.bus }

// handlePerformance serves the process-wide metrics snapshot as JSON. It uses
// the shared metrics.Default() recorder, so values aggregate across the MCP and
// web surfaces into a single point-in-time view.
func (a *App) handlePerformance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, metrics.Default().Snapshot())
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error object. Server-side (5xx) errors are logged in
// full so the operator can diagnose them, but the client only ever receives the
// generic "internal error" message to avoid leaking internal file paths and
// details. Client-side (4xx) validation messages are safe to show and are
// passed through unchanged.
func writeError(w http.ResponseWriter, status int, msg string) {
	if status >= 500 {
		log.Printf("web error: %s", msg)
		msg = "internal error"
	}
	writeJSON(w, status, map[string]string{"error": msg})
}
