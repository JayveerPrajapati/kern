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
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	kerncontext "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/index"
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
	platform   *app.Platform
	inc        *incident.Engine     // prebuilt incident engine (shares a.graph)
	ver        *verification.Engine // prebuilt verification engine (shares a.ix)
	archIndex  *index.Index         // shared index for architecture validation
	memories   *memory.MemoryStore
	inter      *incident.Store
	firewall   *governance.Firewall
	approvals  *governance.ApprovalWorkflow
	taskSvc    *app.TaskService // Phase 5/6/8: task-native analyze/plan/what-if
	dashboardT  *template.Template
	taskDetailT *template.Template
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

	// Build the shared application-services Platform ONCE at startup. It
	// owns the twin-merged graph, memory store, governance firewall, and the
	// context + verification engines. Web handlers delegate to Platform so
	// the orchestration is shared with MCP and CLI instead of duplicated.
	// NewWithGraph stores a pointer to a.graph so freshGraph's in-place swap
	// is visible to the context engine without rebuilding Platform.
	platform, err := app.NewWithGraph(root, ix, &g)
	if err != nil {
		return nil, err
	}

	a := &App{
		root:      root,
		ix:        ix,
		graph:     g,
		platform:  platform,
		memories:  platform.Memory(),
		inter:     incident.NewStore(root),
		firewall:  platform.Firewall(),
		approvals: governance.NewApprovalWorkflow(),
		taskSvc:   app.NewTaskService(platform, nil).WithAgentID("web").WithPRProvider(app.AutoPRProvider()),
		tasks:     agent.NewRegistry(),
		archTTL:   5 * time.Second,
	}
	// Back the /v1/tasks registry with a persisted task store so submitted
	// tasks survive across server restarts and handleV1Task can serve a real,
	// non-empty task registry (returning 404 only when a task is genuinely
	// unknown).
	a.tasks.SetTaskStore(agent.NewTaskStore(root))
	a.ctx = platform.ContextEngine()
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
	a.ver = platform.VerificationEngine()
	a.archIndex = a.ix
	tmpl, err := parseDashboardTemplate()
	if err != nil {
		return nil, err
	}
	a.dashboardT = tmpl

	taskDetailTmpl, err := parseTaskDetailTemplate()
	if err != nil {
		return nil, fmt.Errorf("parse task detail template: %w", err)
	}
	a.taskDetailT = taskDetailTmpl

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
	mux.HandleFunc("/v1/incidents/", a.handleV1Incident)
	mux.HandleFunc("/v1/correlate", a.handleV1Correlate)
	mux.HandleFunc("/v1/learn", a.handleV1Learn)
	mux.HandleFunc("/v1/modernize", a.handleV1Modernize)
	mux.HandleFunc("/v1/execute", a.handleV1Execute)
	mux.HandleFunc("/v1/audit/", a.handleV1Audit)
	mux.HandleFunc("/v1/tasks", a.handleV1TaskSubmit)
	mux.HandleFunc("/v1/tasks/", a.handleV1Task)
	mux.HandleFunc("/v1/artifacts", a.handleV1ArtifactsList)
	mux.HandleFunc("/v1/artifacts/", a.handleV1ArtifactGet)
	mux.HandleFunc("/task/", a.handleTaskDetail)
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

// runtimeSource and boundaryProvider have been migrated to internal/app.Platform,
// which builds the context engine with the runtime source and boundary provider
// internally. Web no longer needs its own copies.

// Bus returns the App's shared event bus so callers (e.g. kern-server) can
// subscribe and fan events out to webhooks or an audit trail.
func (a *App) Bus() *eventbus.Bus { return a.bus }

// ListTasks returns all tasks in the App's task registry (Phase 23b). Used by
// the enterprise server to aggregate task visibility across projects.
func (a *App) ListTasks() []*agent.Task {
	if a.tasks == nil {
		return nil
	}
	return a.tasks.ListTasks()
}

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
