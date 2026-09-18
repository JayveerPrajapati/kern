// Package web is a small, stdlib-only HTTP console that serves the
// project's digital-twin data as JSON plus a minimal server-rendered HTML
// dashboard. Routing uses net/http's ServeMux, the dashboard uses html/template
// with a single embedded template, and all payloads use encoding/json — no
// external dependencies are required. Read-only endpoints are fail-closed:
// build or validation errors surface as 500 JSON rather than panicking.
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
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"crypto/subtle"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/relay"
	"github.com/JayveerPrajapati/kern/internal/service"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"os"
	"strings"
)

// authTokenEnv is the bearer-token env shared with enterprise mode; when set,
// the console requires it on every request.
const authTokenEnv = "KERN_AUTH_TOKEN"

// App holds the project root and the derived console state.
// It delegates routing to an embedded http.ServeMux via ServeHTTP.
type App struct {
	root     string
	mux      *http.ServeMux
	ix       *index.Index
	graph    *intelligence.Graph
	platform *app.Platform
	// rateLimiter caps state-mutating (POST/PUT/PATCH/DELETE) requests per
	// client IP (fixed window, KERN_WEB_RATE_LIMIT req/min, default 600, 0
	// disables). Nil when limiting is disabled. See ratelimit.go.
	rateLimiter *rateLimiter
	// svc is the delivery-mechanism-independent service layer. Handlers
	// delegate core operations (index, graph, memory, governance, security)
	// to it instead of importing the internal engines directly, so the same
	// business logic serves CLI, MCP and web identically.
	svc       *service.Services
	ver       *verification.Engine // prebuilt verification engine (shares a.ix)
	archIndex *index.Index         // shared index for architecture validation
	memories  *memory.MemoryStore
	inter     *incident.Store
	firewall  *governance.Firewall
	approvals *governance.ApprovalWorkflow
	// fileApprovals is the persistent approval store ( exit gate): the
	// agent-team workflow engine persists its approval gates here, so the UI's
	// pending/approve/reject surfaces read and write the SAME store a human
	// uses to unblock a parked workflow (or a `kern approve` does).
	fileApprovals *governance.FileStore
	taskSvc       *app.TaskService // task-native analyze/plan/what-if
	dashboardT    *template.Template
	taskDetailT   *template.Template
	agentsT       *template.Template
	tasksT        *template.Template
	approvalsT    *template.Template
	risksT        *template.Template
	artifactsT    *template.Template
	auditT        *template.Template
	systemMapT    *template.Template
	incidentsT    *template.Template
	efficiencyT   *template.Template
	graphT        *template.Template
	memoryT       *template.Template
	architectureT *template.Template
	evalT         *template.Template
	bus           *eventbus.Bus   // publishes incident/approval events
	tasks         *agent.Registry // agent/task registry for /v1/tasks lookup
	// relay is the cross-process event relay server started by New (nil when
	// another process owns the socket or the relay failed to start). Close()
	// tears it down so relay goroutines/sockets do not leak across App
	// evictions in enterprise mode.
	relay *relay.Server
	// relayUnsub unsubscribes the relay's Broadcast handler from the bus;
	// Close() calls it so an evicted App stops receiving bus events.
	relayUnsub func()
	// closeOnce makes Close airtight under concurrency: the nil-checks below
	// are only safe because closeOnce guarantees exactly one goroutine ever
	// runs the teardown body, so concurrent Close calls can never race the
	// nil-check-and-nil-assign sequence (oracle-gate).
	closeOnce sync.Once

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
	// staleUntil rate-limits the staleness check itself: Stale() walks the
	// whole tree (and re-reads ignore files), so once a "fresh" verdict is
	// produced the next check is skipped for staleCooldown. Burst requests
	// therefore pay zero disk walks; an edit is picked up within ~1s.
	graphMu    sync.RWMutex
	graphVer   int
	staleUntil time.Time
	// rebuilding marks an in-flight background graph rebuild (B10): the
	// single-flight claim is taken under graphMu, then the rebuild runs OFF
	// the lock so concurrent requests are never blocked by it. Callers that
	// arrive while a rebuild is in flight are served the stale snapshot
	// immediately (stale-while-revalidate, same policy as project.Session).
	rebuilding bool
}

// staleCooldown is how long a "fresh" verdict from index.Stale() is trusted
// before the next staleness check (a full tree walk + ignore-file re-read)
// runs. 1s mirrors project.Session's cooldown: burst requests pay zero disk
// walks and edits are reflected within a second.
const staleCooldown = 1 * time.Second

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

	// The event bus is created BEFORE the TaskService so the task/loop lanes
	// publish onto the SAME bus the console streams (/v1/events/stream) and the
	// relay fan out — loop progress, approvals, and lifecycle events are
	// observable instead of being dropped on a nil bus.
	bus := eventbus.New()
	a := &App{
		root:          root,
		ix:            ix,
		graph:         &g,
		platform:      platform,
		svc:           service.New(),
		memories:      platform.Memory(),
		inter:         incident.NewStore(root),
		firewall:      platform.Firewall(),
		approvals:     governance.NewPersistedApprovalWorkflow(root),
		fileApprovals: governance.NewFileStore(root),
		taskSvc:       app.NewTaskService(platform, bus).WithAgentID("web").WithPRProvider(app.AutoPRProvider()),
		tasks:         agent.NewRegistry(),
		bus:           bus,
		archTTL:       5 * time.Second,
		rateLimiter:   rateLimitFromEnv(),
	}
	// Back the /v1/tasks registry with a persisted task store so submitted
	// tasks survive across server restarts and handleV1Task can serve a real,
	// non-empty task registry (returning 404 only when a task is genuinely
	// unknown).
	a.tasks.SetTaskStore(agent.NewTaskStore(root))
	// Cross-process event relay: the first process to bind owns the
	// socket; concurrent servers (or a kern events serve instance) run
	// without one. Purely additive observability — never fatal. The server
	// and its bus subscription are retained on the App so Close() can tear
	// them down (relay goroutines/sockets must not leak across App
	// evictions in enterprise mode).
	if srv, rerr := relay.Start(root); rerr == nil {
		a.relay = srv
		a.relayUnsub = a.bus.Subscribe("", srv.Broadcast) // "" = every kind
		srv.SetPublisher(a.bus.Publish)
	}
	// Bridge the verification engine's architecture events onto the
	// webhook-subscribed bus, and share the persisted guard-CLI event file so
	// cross-process events (e.g. `kern guard check`) replay into this bus.
	// Replay runs BEFORE EnablePersistence so replayed events are not
	// re-appended to the same file on every restart (unbounded growth); a
	// missing events file (first run) is not an error.
	_, _ = a.bus.Replay(filepath.Join(root, ".kern", "events.jsonl"))
	a.bus.EnablePersistence(filepath.Join(root, ".kern", "events.jsonl"))
	platform.WithBus(a.bus)
	// Prebuild the per-request engines ONCE at startup and share the already
	// built index/graph so handlers never re-index the repo per request (this
	// was the #1 bottleneck: /v1/incidents/investigate and /v1/verify each
	// re-ran index.Build). The engines store only read-only references to
	// a.ix / a.graph and are safe for concurrent handler use.
	a.ver = platform.VerificationEngine()
	a.archIndex = a.ix
	if err := a.loadTemplates(); err != nil {
		return nil, err
	}
	a.registerRoutes()
	return a, nil
}

// loadTemplates parses every embedded dashboard template once at startup.
func (a *App) loadTemplates() error {
	tmpl, err := parseDashboardTemplate()
	if err != nil {
		return err
	}
	a.dashboardT = tmpl

	taskDetailTmpl, err := parseTaskDetailTemplate()
	if err != nil {
		return fmt.Errorf("parse task detail template: %w", err)
	}
	a.taskDetailT = taskDetailTmpl

	agentsTmpl, err := parseAgentsTemplate()
	if err != nil {
		return fmt.Errorf("parse agents template: %w", err)
	}
	a.agentsT = agentsTmpl

	tasksTmpl, err := parseTasksTemplate()
	if err != nil {
		return fmt.Errorf("parse tasks template: %w", err)
	}
	a.tasksT = tasksTmpl

	approvalsTmpl, err := parseApprovalsTemplate()
	if err != nil {
		return fmt.Errorf("parse approvals template: %w", err)
	}
	a.approvalsT = approvalsTmpl

	risksTmpl, err := parseRisksTemplate()
	if err != nil {
		return fmt.Errorf("parse risks template: %w", err)
	}
	a.risksT = risksTmpl

	artifactsTmpl, err := parseArtifactsTemplate()
	if err != nil {
		return fmt.Errorf("parse artifacts template: %w", err)
	}
	a.artifactsT = artifactsTmpl

	auditTmpl, err := parseAuditTemplate()
	if err != nil {
		return fmt.Errorf("parse audit template: %w", err)
	}
	a.auditT = auditTmpl

	systemMapTmpl, err := parseSystemMapTemplate()
	if err != nil {
		return fmt.Errorf("parse system map template: %w", err)
	}
	a.systemMapT = systemMapTmpl

	incidentsTmpl, err := parseIncidentsTemplate()
	if err != nil {
		return fmt.Errorf("parse incidents template: %w", err)
	}
	a.incidentsT = incidentsTmpl

	efficiencyTmpl, err := parseEfficiencyTemplate()
	if err != nil {
		return fmt.Errorf("parse efficiency template: %w", err)
	}
	a.efficiencyT = efficiencyTmpl

	graphTmpl, err := parseGraphTemplate()
	if err != nil {
		return fmt.Errorf("parse graph template: %w", err)
	}
	a.graphT = graphTmpl

	memoryTmpl, err := parseMemoryTemplate()
	if err != nil {
		return fmt.Errorf("parse memory template: %w", err)
	}
	a.memoryT = memoryTmpl

	architectureTmpl, err := parseArchitectureTemplate()
	if err != nil {
		return fmt.Errorf("parse architecture template: %w", err)
	}
	a.architectureT = architectureTmpl

	evalTmpl, err := parseEvalTemplate()
	if err != nil {
		return fmt.Errorf("parse eval template: %w", err)
	}
	a.evalT = evalTmpl

	return nil
}

// registerRoutes wires every HTTP route onto the app mux. The route
// table lives here so New() stays readable as the console grows.
func (a *App) registerRoutes() {
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
	mux.HandleFunc("/api/runtime", a.handleRuntimeJSON)
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
	mux.HandleFunc("/v1/incidents", a.handleV1Incidents)
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
	mux.HandleFunc("/v1/approvals/pending", a.handleApprovalsPending)
	mux.HandleFunc("/v1/approve", a.handleApprovalApprove)
	mux.HandleFunc("/v1/reject", a.handleApprovalReject)
	mux.HandleFunc("/v1/events/stream", a.handleV1EventsStream)
	mux.HandleFunc("/task/", a.handleTaskDetail)
	mux.HandleFunc("/agents", a.handleAgents)
	mux.HandleFunc("/tasks", a.handleTasks)
	mux.HandleFunc("/approvals", a.handleApprovals)
	mux.HandleFunc("/risks", a.handleRisks)
	mux.HandleFunc("/artifacts", a.handleArtifacts)
	mux.HandleFunc("/audit", a.handleAudit)
	mux.HandleFunc("/system-map", a.handleSystemMap)
	mux.HandleFunc("/incidents", a.handleIncidentsPage)
	mux.HandleFunc("/efficiency", a.handleEfficiency)
	mux.HandleFunc("/graph", a.handleGraphPage)
	mux.HandleFunc("/memory", a.handleMemoryPage)
	mux.HandleFunc("/architecture", a.handleArchitecturePage)
	mux.HandleFunc("/eval", a.handleEvalPage)
	mux.HandleFunc("/api/risks", a.handleRisksJSON)
	mux.HandleFunc("/api/artifacts", a.handleArtifactsJSON)
	mux.HandleFunc("/api/audit", a.handleAuditJSON)
	mux.HandleFunc("/api/system-map", a.handleSystemMapJSON)
	mux.HandleFunc("/api/efficiency", a.handleEfficiencyJSON)
	a.mux = mux
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
	// P2-10: opt-in bearer gate. The console defaults to a loopback bind
	// (trusted local, no gate). When KERN_AUTH_TOKEN is set — which the
	// server enforces for non-loopback binds — every request must carry
	// "Authorization: Bearer <token>": the console serves state-mutating
	// endpoints (/v1/memory writes, /api/approvals/approve|reject, incident
	// ingestion) and LLM work, so a network-exposed console without auth is
	// an unauthenticated write/execute surface. Constant-time compare, same
	// as the enterprise gate.
	if !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// P2-11: per-IP rate cap on state-mutating requests (POST/PUT/PATCH/
	// DELETE). Fixed window (default 600 req/min per IP, KERN_WEB_RATE_LIMIT,
	// 0 disables). Read-only dashboard/API GETs are not throttled so the
	// console stays snappy under normal polling; only the write/LLM-heavy
	// surface is bounded against runaway clients.
	if a.rateLimiter != nil && mutatingMethod(r.Method) {
		if ok, retry := a.rateLimiter.allow(clientIP(r), time.Now()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}
	// P2-12: cross-origin / DNS-rebinding guard on state-mutating requests.
	// Only browser-initiated requests (those carrying an Origin header) are
	// checked; curl/SDK/bearer-token clients never send Origin and always
	// pass. The allowlist is recomputed per request so KERN_WEB_ALLOWED_HOSTS
	// changes apply without a restart.
	if mutatingMethod(r.Method) {
		if reason := csrfViolation(r, allowedHosts()); reason != "" {
			http.Error(w, reason, http.StatusForbidden)
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}

// authorized reports whether the request passes the opt-in bearer gate. When
// KERN_AUTH_TOKEN is unset there is no gate (loopback default). When set, the
// request must present a matching "Authorization: Bearer <token>" header.
func (a *App) authorized(r *http.Request) bool {
	token := os.Getenv(authTokenEnv)
	if token == "" {
		return true
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

// freshGraph returns the current knowledge graph and index, rebuilding both if
// the project has changed since the last build. Staleness is detected via the
// index package's Stale() (file set + content-hash check), rate-limited by
// staleCooldown so burst requests pay zero disk walks.
//
// B10 — stale-while-revalidate: the rebuild is claimed under the write lock
// but runs OFF it (single-flight), so the first request after an edit no
// longer blocks ALL web API requests for the full rebuild (30-90s on large
// repos). The triggering caller waits for the fresh index; concurrent callers
// are served the stale snapshot immediately. The fresh state is swapped in
// atomically under the write lock.
func (a *App) freshGraph() (*intelligence.Graph, *index.Index) {
	a.graphMu.RLock()
	if time.Now().Before(a.staleUntil) {
		g, ix := a.graph, a.ix
		a.graphMu.RUnlock()
		return g, ix
	}
	if !a.ix.Stale() {
		a.staleUntil = time.Now().Add(staleCooldown)
		g, ix := a.graph, a.ix
		a.graphMu.RUnlock()
		return g, ix
	}
	a.graphMu.RUnlock()

	// Stale. Claim the rebuild under the write lock (single-flight), then run
	// it OFF the lock so no request is ever blocked by the reindex.
	a.graphMu.Lock()
	if a.rebuilding {
		// Another rebuild is in flight: serve the stale snapshot now; it
		// atomically swaps in the fresh graph when it finishes.
		g, ix := a.graph, a.ix
		a.graphMu.Unlock()
		return g, ix
	}
	if !a.ix.Stale() {
		// Another caller rebuilt while we waited for the write lock.
		a.staleUntil = time.Now().Add(staleCooldown)
		g, ix := a.graph, a.ix
		a.graphMu.Unlock()
		return g, ix
	}
	a.rebuilding = true
	a.graphMu.Unlock()

	// Rebuild off the lock: concurrent requests are served the stale graph
	// meanwhile (see the a.rebuilding branch above).
	nix, err := a.rebuildIndex()

	a.graphMu.Lock()
	if err == nil && nix != nil {
		a.ix = nix
		ng := intelligence.FromIndex(nix)
		a.graph = &ng
		a.archIndex = nix
		// The verification engine captured the pre-swap index at construction;
		// rebuild it against the new index so future verify calls are not
		// stale (same constructor path used in New).
		if a.ver != nil {
			a.ver = verification.NewEngineWithIndex(a.root, nix)
		}
		a.graphVer++
	}
	a.rebuilding = false
	a.staleUntil = time.Now().Add(staleCooldown)
	g, ix := a.graph, a.ix
	a.graphMu.Unlock()
	return g, ix
}

// rebuildIndex builds a fresh index for a.root. It prefers an incremental
// Update (re-parsing only changed files, reusing symbols/edges of unchanged
// ones — the same policy as the session's rebuild) whenever the current index
// is usable as the prior, falling back to a full Build on any Update failure.
// The swap semantics in freshGraph are unchanged either way.
func (a *App) rebuildIndex() (*index.Index, error) {
	if a.ix != nil {
		if uix, uerr := index.Update(a.root, a.ix); uerr == nil && uix != nil {
			return uix, nil
		}
	}
	return index.Build(a.root)
}

// runtimeSource and boundaryProvider have been migrated to internal/app.Platform,
// which builds the context engine with the runtime source and boundary provider
// internally. Web no longer needs its own copies.

// Bus returns the App's shared event bus so callers (e.g. kern-server) can
// subscribe and fan events out to webhooks or an audit trail.
func (a *App) Bus() *eventbus.Bus { return a.bus }

// Close tears down the background resources New() started: the cross-process
// event relay (listener, socket, client connections) and its bus subscription,
// so an App that is dropped (e.g. evicted from the enterprise cache) does not
// leak relay goroutines or sockets. It is idempotent and best-effort — a
// failure to close one resource is logged and does not stop the others — and
// it never panics, so callers can invoke it defensively before discarding an
// App. The App is not usable for serving after Close (New must be called again
// to rebuild it).
//
// Airtight under concurrency: the whole teardown body runs inside a sync.Once,
// so any number of concurrent Close calls (e.g. an eviction race between the
// enterprise cache and a shutdown path) execute the teardown exactly once and
// never race the nil checks (oracle-gate). Waiters on the once block until the
// first Close finishes, then return nil.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.relay != nil {
			// relay.Close is idempotent; it removes the socket file too, so a
			// rebuilt App on the same root can rebind.
			a.relay.Close()
			a.relay = nil
		}
		if a.relayUnsub != nil {
			// The unsubscriber is idempotent; nil it so a second Close is a no-op.
			a.relayUnsub()
			a.relayUnsub = nil
		}
	})
	return nil
}

// ListTasks returns all tasks in the App's task registry. Used by
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
func writeJSON(w http.ResponseWriter, status int, v any) {
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
