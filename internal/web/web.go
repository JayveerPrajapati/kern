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
	"context"
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
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/relay"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"os"
	"strings"
)

// authTokenEnv is the bearer-token env shared with enterprise mode; when set,
// the console requires it on every request.
const authTokenEnv = "KERN_AUTH_TOKEN"

// ToolServer is the in-process tool dispatch the /v1/tools/{name} passthrough
// route delegates to. It is satisfied structurally by *mcp.Server
// (CallToolGoverned — the full governed dispatch path MCP clients hit:
// KERN_TOOLS allowlist, root confinement, RBAC). internal/web cannot import
// internal/mcp (the mcp → org → enterprise → web import cycle), so the
// implementation is injected: cmd/kern registers the factory once at startup
// and every App built afterwards — single-project and enterprise alike — gets
// its own dispatch server. When no factory is registered the route is
// unavailable (503).
type ToolServer interface {
	CallToolGoverned(ctx context.Context, name string, args map[string]any) (string, error)
	// Close releases the server's background resources (index sessions, file
	// watchers); App.Close calls it so nothing leaks across App evictions.
	Close()
}

// toolServerFactory builds the per-App in-process tool dispatch; nil by
// default (route unavailable) until SetToolServerFactory wires the real one.
// The factory is ROOT-AWARE: it receives the App's project root so the tool
// server it builds confines every tool call to THAT App's tree (finding 1) —
// an unrooted factory would let one cwd-rooted server back every project
// console and run enterprise tool calls in the wrong directory.
var toolServerFactory = func(root string) ToolServer { return nil }

// SetToolServerFactory registers the factory that builds each App's in-process
// tool dispatch server. The kern binary calls it at startup (cmd/kern); tests
// that exercise the /v1/tools/{name} route call it themselves. Registration is
// idempotent and must happen before App construction.
func SetToolServerFactory(f func(root string) ToolServer) {
	if f != nil {
		toolServerFactory = f
	}
}

// App holds the project root and the derived console state.
// It delegates routing to an embedded http.ServeMux via ServeHTTP.
type App struct {
	root     string
	mux      *http.ServeMux
	ix       *index.Index
	graph    *intel.Graph
	platform *app.Platform
	// rateLimiter caps state-mutating (POST/PUT/PATCH/DELETE) requests per
	// client IP (fixed window, KERN_WEB_RATE_LIMIT req/min, default 600, 0
	// disables). Nil when limiting is disabled. See ratelimit.go.
	rateLimiter *rateLimiter
	ver         *verification.Engine // prebuilt verification engine (shares a.ix)
	archIndex   *index.Index         // shared index for architecture validation
	memories    *memory.MemoryStore
	inter       *incident.Store
	firewall    *governance.Firewall
	approvals   *governance.ApprovalWorkflow
	// userRole resolves an actor's org role for the approvals RBAC check
	// (Feature Batch G). Nil when no user registry is wired — the
	// single-project/local console flow, which skips the RBAC check and
	// preserves the historical behavior. Enterprise mode injects it via
	// SetUserRoleLookup so approve/reject enforce governance.RequireOrgRole.
	userRole func(id string) (string, bool)
	// fileApprovals is the persistent approval store ( exit gate): the
	// agent-team workflow engine persists its approval gates here, so the UI's
	// pending/approve/reject surfaces read and write the SAME store a human
	// uses to unblock a parked workflow (or a `kern approve` does).
	fileApprovals *governance.FileStore
	taskSvc       *app.TaskService // task-native analyze/plan/what-if
	// tools is the in-process tool dispatch behind the single REST passthrough
	// route POST /v1/tools/{name}: it fronts the full catalog through the same
	// governed dispatch path MCP clients hit (allowlist, root confinement,
	// RBAC) with zero HTTP/stdio transport. internal/web cannot import
	// internal/mcp itself — the mcp → org → enterprise → web cycle — so the
	// server is injected via SetToolServerFactory (cmd/kern registers the real
	// factory at startup; see ToolServer). Closed with the App so its
	// background resources (index sessions, watchers) do not leak.
	tools         ToolServer
	dashboardT    *template.Template
	taskDetailT   *template.Template
	agentsT       *template.Template
	tasksT        *template.Template
	approvalsT    *template.Template
	orgApprovalsT *template.Template
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
	benchT        *template.Template
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
	// policies is the last policy set applied via SetPolicies; freshGraph
	// re-applies it to the firewall of any rebuilt platform so org policies
	// survive index-generation swaps (a rebuilt firewall starts from
	// governance.DefaultPolicies). polMu guards it against a concurrent
	// SetPolicies racing a rebuild in flight.
	policies []domain.Policy
	polMu    sync.Mutex
	// commMu/commVer/commCache cache the per-graph-generation Communities +
	// Hubs computation (label propagation up to 30 iterations + hub ranking)
	// shared by buildOverview/buildGraph/buildSystemMap — the archTTL pattern
	// keyed on graphVer instead of time, so a dashboard render never computes
	// them more than once per rebuild.
	commMu    sync.Mutex
	commVer   int
	commCache *communitiesCache
}

// staleCooldown is how long a "fresh" verdict from index.Stale() is trusted
// before the next staleness check (a full tree walk + ignore-file re-read)
// runs. 1s mirrors project.Session's cooldown: burst requests pay zero disk
// walks and edits are reflected within a second.
const staleCooldown = 1 * time.Second

// communitiesCache holds the once-per-graph-generation community and hub
// computations shared by the dashboard/API builders (see
// graphCommunitiesHubs).
type communitiesCache struct {
	comms []intel.Community
	hubs  []intel.Hub
}

// cachedHubsLimit is the largest hub limit any web builder passes to
// intel.Hubs; the shared cache is computed at this width and truncated per
// call. intel.Hubs sorts before truncating, so the top-N of the cached list
// is byte-identical to intel.Hubs(ix, N) for any N <= cachedHubsLimit.
const cachedHubsLimit = 50

// graphCommunitiesHubs returns the community list and the top hubs for the
// CURRENT graph generation, computing them once per rebuild (keyed on
// graphVer) and caching the result for every builder that needs them. The
// index is immutable once published (freshGraph swaps pointers, it never
// mutates a generation), so the cached values stay valid for the whole
// generation.
func (a *App) graphCommunitiesHubs() ([]intel.Community, []intel.Hub) {
	a.graphMu.RLock()
	ver := a.graphVer
	ix := a.ix
	a.graphMu.RUnlock()

	a.commMu.Lock()
	defer a.commMu.Unlock()
	if a.commCache != nil && a.commVer == ver {
		return a.commCache.comms, a.commCache.hubs
	}
	comms := intel.Communities(ix)
	hubs := intel.Hubs(ix, cachedHubsLimit)
	a.commCache = &communitiesCache{comms: comms, hubs: hubs}
	a.commVer = ver
	return comms, hubs
}

// hubsTop truncates the cached top-hubs list to limit, mirroring intel.Hubs'
// own truncation semantics (see cachedHubsLimit).
func hubsTop(hubs []intel.Hub, limit int) []intel.Hub {
	if len(hubs) > limit {
		return hubs[:limit]
	}
	return hubs
}

// New builds the digital-twin state for root and returns a ready-to-serve App.
// It never panics: any build error is wrapped and returned to the caller.
func New(root string) (*App, error) {
	// Load the persisted index before building: a fresh index on disk (from a
	// prior serve, `kern index`, or the MCP session) makes restarts
	// effectively instant instead of re-indexing the whole tree on every
	// `kern serve` — the same load-first pattern project.Session uses. A
	// missing or stale index is rebuilt (incrementally when a prior loads
	// cleanly, else a full Build) and persisted. New never returns without a
	// usable index: any load failure falls back to Build and its error is
	// surfaced exactly as before.
	ix, err := loadOrBuildIndex(root)
	if err != nil {
		return nil, err
	}
	g := intel.FromIndex(ix)

	// Build the shared application-services Platform ONCE at startup. It
	// owns the twin-merged graph, memory store, governance firewall, and the
	// context + verification engines. Web handlers delegate to Platform so
	// the orchestration is shared with MCP and CLI instead of duplicated.
	// a.graph and platform.Graph() are the SAME pointer here (NewWithGraph
	// stores the caller's graph and twin.Merge runs in place at construction),
	// so the dashboard and the context engine always agree on dimensions;
	// freshGraph preserves that invariant after an index rebuild by rebuilding
	// the Platform and pointing a.graph at the new Platform's graph.
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
		memories:      platform.Memory(),
		inter:         incident.NewStore(root),
		firewall:      platform.Firewall(),
		approvals:     governance.NewPersistedApprovalWorkflow(root),
		fileApprovals: governance.NewFileStore(root),
		taskSvc:       app.NewTaskService(platform, bus).WithAgentID("web").WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true),
		tools:         toolServerFactory(root),
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

	orgApprovalsTmpl, err := parseOrgApprovalsTemplate()
	if err != nil {
		return fmt.Errorf("parse org approvals template: %w", err)
	}
	a.orgApprovalsT = orgApprovalsTmpl

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

	benchTmpl, err := parseBenchmarksTemplate()
	if err != nil {
		return fmt.Errorf("parse benchmarks template: %w", err)
	}
	a.benchT = benchTmpl

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
	mux.HandleFunc("/v1/tools/", a.handleV1ToolCall)
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
	mux.HandleFunc("/org-approvals", a.handleOrgApprovals)
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
	mux.HandleFunc("/benchmarks", a.handleBenchmarksPage)
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
	// (trusted local, no gate). Enforcement of the non-loopback case now
	// lives in the CLI layer: `kern serve` / kern-server refuse to bind a
	// non-loopback address without KERN_AUTH_TOKEN (single-project mode),
	// and enterprise mode fails closed with 503 until the token is set. So
	// when KERN_AUTH_TOKEN is present, every request must carry
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

// indexStatus reports whether a background graph rebuild (freshGraph's
// stale-while-revalidate) is in flight, plus the current graph version. It is
// the status field /api/health serves so the console's pending-state line can
// tell a viewer that the page they are looking at was served from the stale
// snapshot and will pick up edits once the rebuild lands.
func (a *App) indexStatus() map[string]any {
	a.graphMu.RLock()
	defer a.graphMu.RUnlock()
	return map[string]any{"building": a.rebuilding, "version": a.graphVer}
}

// freshGraph returns the current knowledge graph and index, rebuilding both if
// the project has changed since the last build. Staleness is detected via the
// index package's Stale() (file set + content-hash check), rate-limited by
// staleCooldown so burst requests pay zero disk walks.
//
// Lock discipline (ported from project.Session.Index): the Stale() walk runs
// OFF the lock on a local copy of the index pointer, so concurrent requests
// are never blocked behind it and staleUntil is only ever written under the
// write lock (never while other readers hold RLock — the previous
// write-under-RLock was a data race between concurrent RLock holders). The
// rebuild itself is claimed under the write lock (single-flight) but runs OFF
// it — B10 stale-while-revalidate: the first request after an edit waits for
// the fresh index; concurrent callers are served the stale snapshot
// immediately. The fresh state is swapped in atomically under the write lock.
func (a *App) freshGraph() (*intel.Graph, *index.Index) {
	// Fast path: within the staleness cooldown — or while a rebuild is in
	// flight (stale-while-revalidate) — serve the current snapshot with no
	// disk walk.
	a.graphMu.RLock()
	if time.Now().Before(a.staleUntil) || a.rebuilding {
		g, ix := a.graph, a.ix
		a.graphMu.RUnlock()
		return g, ix
	}
	ix := a.ix
	a.graphMu.RUnlock()

	// Staleness check on the local copy, OFF the lock (a full tree walk; the
	// served index is immutable once published, so the copy cannot change
	// under us).
	stale := ix.Stale()

	// Reconcile with the current state: the world may have changed while the
	// check ran (a concurrent rebuild landed, another request refreshed the
	// cooldown, or a rebuild was claimed).
	a.graphMu.Lock()
	if time.Now().Before(a.staleUntil) {
		// Another caller refreshed or rebuilt while we walked the tree: serve
		// the current snapshot.
		g, ix := a.graph, a.ix
		a.graphMu.Unlock()
		return g, ix
	}
	if a.rebuilding {
		// A rebuild is in flight: serve the stale snapshot now; it atomically
		// swaps in the fresh graph when it finishes.
		g, ix := a.graph, a.ix
		a.graphMu.Unlock()
		return g, ix
	}
	if !stale && a.ix == ix {
		// The copy we checked is still the served index and judged fresh:
		// record the cooldown (under the write lock only) and serve it.
		a.staleUntil = time.Now().Add(staleCooldown)
		g, ix := a.graph, a.ix
		a.graphMu.Unlock()
		return g, ix
	}
	// Stale: claim the rebuild (single-flight) and run it OFF the lock so no
	// request is ever blocked by the reindex.
	a.rebuilding = true
	a.graphMu.Unlock()

	// Rebuild off the lock: concurrent requests are served the stale graph
	// meanwhile (see the a.rebuilding branches above). The platform rebuild
	// (twin merge, memory/firewall setup, audit replay, TaskService) runs
	// here too so the swap under the lock stays pure pointer assignment.
	nix, err := a.rebuildIndex()
	var newPlat *app.Platform
	var newSvc *app.TaskService
	if err == nil && nix != nil {
		newPlat, newSvc = a.rebuildPlatform(nix)
	}

	a.graphMu.Lock()
	if err == nil && nix != nil && newPlat != nil {
		// Pointer swap under the write lock. Old generations stay intact for
		// in-flight readers — never in-place mutation of maps — and every web
		// surface now serves the SAME generation: index, graph (the
		// platform's twin-merged one), arch index, verification engine,
		// platform and TaskService.
		a.ix = nix
		a.graph = newPlat.Graph()
		a.archIndex = nix
		a.platform = newPlat
		a.memories = newPlat.Memory()
		a.firewall = newPlat.Firewall()
		a.ver = newPlat.VerificationEngine()
		a.taskSvc = newSvc
		a.graphVer++
	}
	a.rebuilding = false
	a.staleUntil = time.Now().Add(staleCooldown)
	g, ix := a.graph, a.ix
	a.graphMu.Unlock()
	return g, ix
}

// rebuildPlatform constructs the Platform (and its TaskService) for a new
// index generation, re-applying the App's custom state so the rebuilt surface
// is indistinguishable from the one New() builds: the event-bus wiring (New
// does platform.WithBus(a.bus) at startup) and any org policies applied via
// SetPolicies — a rebuilt firewall starts from governance.DefaultPolicies and
// must be re-seeded. Runs OFF the graph lock: construction runs the twin
// merge, memory/firewall setup and audit replay, which must never block
// concurrent requests. Returns a nil platform when construction fails;
// freshGraph then keeps the previous generation (fail closed) instead of
// serving surfaces that disagree on index generations.
//
// The TaskService is rebuilt with it because it holds a pointer to the
// Platform (task.go); a stale service would keep serving the previous
// generation's graph forever. Its in-memory task state (workflow runs,
// ephemeral analysis tasks) is generation-scoped and does not survive a
// rebuild; persisted task records do, via the file-backed store.
func (a *App) rebuildPlatform(nix *index.Index) (*app.Platform, *app.TaskService) {
	plat, err := app.NewWithIndex(a.root, nix)
	if err != nil || plat == nil {
		log.Printf("web: platform rebuild failed: %v", err)
		return nil, nil
	}
	// Re-bridge the event bus so the new platform's engines (verification
	// included) publish onto the SAME bus the console streams and the relay
	// fan out (mirror New()'s platform.WithBus(a.bus)).
	plat.WithBus(a.bus)
	a.polMu.Lock()
	policies := a.policies
	a.polMu.Unlock()
	if len(policies) > 0 {
		plat.Firewall().WithPolicies(policies)
	}
	svc := app.NewTaskService(plat, a.bus).WithAgentID("web").WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true)
	return plat, svc
}

// loadOrBuildIndex returns the project's symbol index, reusing the persisted
// copy when fresh and rebuilding (incrementally when a previous index loads
// cleanly, else a full Build) when missing or stale — the same cascade
// project.Session.rebuildIndex runs. The result is persisted before
// returning, so a restart with a fresh on-disk index is instant and the next
// rebuild has a private prior to update from. A full-build failure is
// returned to the caller (New surfaces it exactly as before).
func loadOrBuildIndex(root string) (*index.Index, error) {
	var prev *index.Index
	if index.SQLiteEnabled() {
		// SQLite is the persistent store for concurrent access (WAL). Prefer
		// it over the JSON cache; rebuild when absent or stale.
		if ix, err := index.LoadSQLite(root); err == nil && ix != nil {
			if !ix.Stale() {
				return ix, nil
			}
			if prev == nil {
				prev = ix
			}
		}
	}
	if ix, err := index.Load(root); err == nil && ix != nil {
		if !ix.Stale() {
			return ix, nil
		}
		if prev == nil {
			prev = ix
		}
	}
	var ix *index.Index
	if prev != nil {
		// Large change sets make incremental Update more expensive than a
		// clean Build — Update would re-parse nearly every file — so skip it
		// and rebuild (the policy, mirroring project.Session).
		cur, herr := index.FileHashes(root)
		if herr != nil {
			// Unprovable freshness (scan error): fail closed with a full
			// rebuild rather than incrementally updating a possibly-stale prev.
			cur = nil
		}
		if cur != nil && len(index.Diff(prev.FileHashes, cur)) <= index.CatchUpMaxChanges {
			if uix, uerr := index.Update(root, prev); uerr == nil && uix != nil {
				ix = uix
			}
		}
	}
	if ix == nil {
		var berr error
		ix, berr = index.Build(root)
		if berr != nil {
			return nil, berr
		}
	}
	if index.SQLiteEnabled() {
		// Persist to SQLite for concurrent access; the JSON cache remains as
		// a fallback for builds without the sqlite tag. Synchronous here
		// (unlike project.Session's async JSON save) so the very next load —
		// a restart, or the next rebuild's private prior — finds the copy:
		// web rebuilds are rare (once per staleness cooldown), never per
		// request.
		if serr := index.SaveSQLite(root, ix); serr == nil {
			return ix, nil
		}
	}
	if serr := ix.Save(); serr != nil {
		log.Printf("web: persist index %s: %v", root, serr)
	}
	return ix, nil
}

// rebuildIndex builds a fresh index for a.root, run OFF the graph lock by
// freshGraph. It loads a PRIVATE previous index from disk (never the live
// a.ix) as the incremental-Update base: index.Update mutates its prev
// argument (initMaps + reindexByFile reassign SymbolsByFile/kindIdx), and the
// live index may still be read by concurrent requests, so passing it would be
// a data race — the same hazard project.Session documents. The cascade is
// loadOrBuildIndex: prefer an incremental Update over a full Build whenever a
// previous index loads cleanly; any failure falls back to Build. The result
// is persisted before returning so the next restart (and the next rebuild's
// private prior) reuses it.
func (a *App) rebuildIndex() (*index.Index, error) {
	return loadOrBuildIndex(a.root)
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
// SetUserRoleLookup wires the org user registry's role lookup into the
// approvals RBAC check (Feature Batch G). When set, POST
// /api/approvals/approve and /api/approvals/reject enforce
// governance.RequireOrgRole on the approver's role; unknown approvers are
// denied. When never set (the single-project/local console), the historical
// no-RBAC behavior is preserved. Enterprise mode calls this when it builds a
// project's web.App.
func (a *App) SetUserRoleLookup(lookup func(id string) (string, bool)) {
	a.userRole = lookup
}

// SetPolicies swaps the risk policies this App's firewall enforces. It is
// the seam enterprise mode uses to build each project's firewall from the
// org policy instead of the defaults: the firewall's assessor is replaced
// (the same WithPolicies swap governance.Firewall exposes), so every
// governance surface — the /api/governance and /api/risks builders and the
// enforcement paths themselves — reflects the given set. The swap is guarded
// by the firewall's own mutex, so it is safe to call while the App is
// serving. When never called the App enforces governance.DefaultPolicies
// (the classic single-project behavior, byte-for-byte).
func (a *App) SetPolicies(policies []domain.Policy) {
	if a.firewall == nil {
		return
	}
	a.firewall.WithPolicies(policies)
	// Retain the applied set so freshGraph can re-seed the firewall of a
	// rebuilt platform (a rebuilt firewall starts from DefaultPolicies).
	a.polMu.Lock()
	a.policies = policies
	a.polMu.Unlock()
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.tools != nil {
			// Stop the in-process MCP server's background resources (index
			// sessions, file watchers) alongside the relay.
			a.tools.Close()
			a.tools = nil
		}
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
