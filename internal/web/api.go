package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// v1AnalyzeResponse wraps the structured context packet with its rendered
// human-readable text so callers can consume either form.
type v1AnalyzeResponse struct {
	Packet domain.ContextPacket `json:"packet"`
	Text   string               `json:"text"`
	TaskID string               `json:"task_id,omitempty"`
}

// v1PlanResponse wraps the structured Plan with its rendered text.
type v1PlanResponse struct {
	Plan   domain.Plan `json:"plan"`
	Text   string      `json:"text"`
	TaskID string      `json:"task_id,omitempty"`
}

// symbolErrorStatus maps a symbol-resolution failure to its HTTP status:
// 404 when the change names a symbol that does not exist in the
// project's index (the message carries real candidates), 400 when no symbol
// could be identified at all. Zero means the error is not a resolution
// failure and should stay a 500. Without this, writeError masks these
// descriptive errors into a bare "internal error".
func symbolErrorStatus(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no symbol named"):
		return http.StatusNotFound
	case strings.Contains(msg, "could not identify a symbol"):
		return http.StatusBadRequest
	}
	return 0
}

// handleV1Analyze analyzes a proposed change via the TaskService so an
// authoritative Task record is created, the lifecycle is recorded, and the
// context packet is attached. POST only. routes through TaskService
// instead of calling a.ctx directly.
func (a *App) handleV1Analyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.freshGraph()
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	t, text, err := a.taskSvc.Analyze(req.Change)
	if err != nil {
		if st := symbolErrorStatus(err); st != 0 {
			writeError(w, st, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var pkt domain.ContextPacket
	if t.ContextPacket != nil {
		pkt = *t.ContextPacket
	}
	writeJSON(w, http.StatusOK, v1AnalyzeResponse{Packet: pkt, Text: text, TaskID: t.ID})
}

// handleGovernanceMetrics aggregates AI-governance metrics from the
// subsystems currently wired onto the App and returns them as a metrics.Snapshot
// with the governance fields populated. It starts from the live process-wide
// recorder snapshot (so performance/self-observability counts ride along) and
// overrides only the governance fields. Sources that are not wired on this App
// contribute 0 rather than forcing new dependencies:
// - AgentCount: registered agents from agent.Registry
// - TaskCount: tasks tracked by the agent registry
// - BlocksCount: audit entries with a blocked/denied decision
// - OverridesCount: audit entries with a human "approved" decision (override)
// - ViolationsCount: cached architecture validation report violations
// - AvgConfidence: the task's ImpactReport.Confidence averaged over all tasks
// known to the TaskService (a.taskSvc.List()). Each what-if/impact task
// stores whatif.Impact.Confidence into its ImpactReport, so this reflects
// the deterministic confidence of real impact estimates. Tasks without an
// ImpactReport (e.g. bare analyze tasks) are skipped; 0 when no task
// carries a confidence.
func (a *App) handleGovernanceMetrics(w http.ResponseWriter, r *http.Request) {
	snap := metrics.Default().Snapshot()

	if a.tasks != nil {
		snap.AgentCount = len(a.tasks.All())
		snap.TaskCount = a.tasks.TaskCount()
	}

	if a.taskSvc != nil {
		var sum float64
		var n int
		for _, t := range a.taskSvc.List() {
			if t != nil && t.ImpactReport != nil {
				sum += t.ImpactReport.Confidence
				n++
			}
		}
		if n > 0 {
			snap.AvgConfidence = sum / float64(n)
		}
	}

	if a.firewall != nil {
		// B8: O(1) lifetime counters instead of scanning the full audit log
		// (which is also retention-capped in memory now).
		l := a.firewall.AuditLog()
		snap.BlocksCount = int(l.BlocksCount())
		snap.OverridesCount = int(l.OverridesCount())
	}

	if rep, err := a.buildArchitecture(); err == nil && rep != nil {
		snap.ViolationsCount = len(rep.Violations)
	}

	writeJSON(w, http.StatusOK, snap)
}

// handleV1Plan produces a structured Plan via the TaskService.Plan workflow
// (analyze → memory → impact → risk → architecture → plan artifact). POST
// only. distinct from /v1/analyze — returns a domain.Plan.
func (a *App) handleV1Plan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.freshGraph()
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	t, plan, text, err := a.taskSvc.Plan(req.Change)
	if err != nil {
		if st := symbolErrorStatus(err); st != 0 {
			writeError(w, st, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v1PlanResponse{Plan: plan, Text: text, TaskID: t.ID})
}

// handleV1WhatIf simulates a hypothetical change via TaskService.WhatIf so an
// authoritative Task record is created. routes through TaskService
// instead of calling whatif.Simulate directly.
func (a *App) handleV1WhatIf(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Change    string `json:"change"`
		Kind      string `json:"kind"`
		NewTarget string `json:"new_target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	kind := whatif.ChangeKind(req.Kind)
	if req.Kind == "" {
		kind = whatif.RemoveSymbol
	}
	t, _, err := a.taskSvc.WhatIf(kind, req.Change, req.NewTarget)
	if err != nil {
		if st := symbolErrorStatus(err); st != 0 {
			writeError(w, st, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var imp whatif.Impact
	if t.ImpactReport != nil {
		imp = *t.ImpactReport
	}
	writeJSON(w, http.StatusOK, struct {
		whatif.Impact
		TaskID string `json:"task_id,omitempty"`
	}{Impact: imp, TaskID: t.ID})
}

// handleV1Impact produces the 11-question deterministic ImpactReport via
// TaskService.Impact. distinct from /v1/what-if — returns a
// domain.ImpactReport (graph-driven, no LLM).
func (a *App) handleV1Impact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	t, rep, _, err := a.taskSvc.Impact(req.Change)
	if err != nil {
		if st := symbolErrorStatus(err); st != 0 {
			writeError(w, st, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// F-WC1: bound the wire payload — the transitive closures (WhatItCalls
	// especially) can reach thousands of qualified names (1.5MB measured on
	// a real repo) where every other surface truncates. Each list is capped
	// at v1ImpactListCap entries; `truncated` reports the FULL original
	// sizes so callers know what was cut.
	const v1ImpactListCap = 200
	truncated := map[string]int{}
	capList := func(field string, in []string) []string {
		if len(in) <= v1ImpactListCap {
			return in
		}
		truncated[field] = len(in)
		out := make([]string, v1ImpactListCap)
		copy(out, in[:v1ImpactListCap])
		return out
	}
	rep.WhatItCalls = capList("what_it_calls", rep.WhatItCalls)
	rep.WhoCalls = capList("who_calls", rep.WhoCalls)
	rep.ServicesDepend = capList("services_depend", rep.ServicesDepend)
	rep.APIsAffected = capList("apis_affected", rep.APIsAffected)
	rep.DataStoresAffected = capList("data_stores_affected", rep.DataStoresAffected)
	rep.EventsAffected = capList("events_affected", rep.EventsAffected)
	rep.TestsCover = capList("tests_cover", rep.TestsCover)
	writeJSON(w, http.StatusOK, struct {
		domain.ImpactReport
		TaskID    string         `json:"task_id,omitempty"`
		Truncated map[string]int `json:"truncated,omitempty"`
	}{ImpactReport: rep, TaskID: t.ID, Truncated: truncated})
}

// verifyTypes is a lenient decoder for the /v1/verify "types" field. The
// canonical wire shape is a JSON array of strings (e.g. ["build","test"]); a
// single comma-separated string (e.g. "build,test") is also accepted for
// backward compatibility with older string-style clients. Each element is
// trimmed and empty entries are dropped.
type verifyTypes []string

// UnmarshalJSON accepts either a JSON array of strings or a single string.
func (v *verifyTypes) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		*v = out
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	out := make([]string, 0)
	for _, s := range strings.Split(str, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	*v = out
	return nil
}

// decodeVerifyTypesBody decodes a /v1/verify request body and returns the
// requested verification types. An absent "types" field yields nil so the
// caller can apply the default. The decode error is surfaced (not swallowed)
// so malformed requests fail loudly instead of silently dropping types.
func decodeVerifyTypesBody(r io.Reader) ([]string, error) {
	var req struct {
		Types verifyTypes `json:"types"`
	}
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return nil, err
	}
	return []string(req.Types), nil
}

// handleV1Verify runs the verification engine against the requested types
// (default "build,test") and returns the unified result.
func (a *App) handleV1Verify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	types, err := decodeVerifyTypesBody(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(types) == 0 {
		types = []string{"build", "test"}
	}
	_, res, err := a.taskSvc.Verify(types)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleV1Memory lists typed engineering memories (GET) or records a new one
// (POST). Any other method returns 405.
func (a *App) handleV1Memory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.buildMemory())
	case http.MethodPost:
		var req struct {
			Content string   `json:"content"`
			Type    string   `json:"type"`
			Scope   string   `json:"scope"`
			Source  string   `json:"source"`
			Tags    []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}
		m, err := a.memories.Add(domain.Memory{
			Content: req.Content,
			Type:    domain.MemoryType(req.Type),
			Scope:   req.Scope,
			Source:  req.Source,
			Tags:    req.Tags,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleV1Graph returns a single graph entity with its reverse and forward
// dependency neighborhoods. The trailing path segment is the node ID.
func (a *App) handleV1Graph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	entity, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/graph/"))
	if err != nil || strings.TrimSpace(entity) == "" {
		writeError(w, http.StatusBadRequest, "invalid entity")
		return
	}
	var node *domain.Node
	g, _ := a.freshGraph()
	// O(1) node lookup via the graph's cached byID map (no linear scan of
	// g.Nodes).
	if n, ok := g.NodeByID(entity); ok {
		node = &n
	}
	nodeID := entity
	if node == nil {
		// The trailing segment may be a symbol NAME ("FindUser") rather
		// than a node ID ("pkg.FindUser"). Resolve it through the prebuilt
		// graph engine (never build a new index) and return the neighborhood
		// of the resolved node; unknown entities keep the 404.
		if id, ok := g.ResolveNodeID(entity); ok {
			nodeID = id
			if n, ok := g.NodeByID(id); ok {
				node = &n
			}
		}
	}
	if node == nil {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node":         *node,
		"who_calls":    g.WhoCalls(nodeID),
		"depends_on":   g.WhatDependsOn(nodeID),
		"depends_upon": g.WhatDoesXDependOn(nodeID),
	})
}

// handleV1Context analyzes a proposed change and returns the raw ContextPacket
// (the "context" view) with no rendered summary wrapper. It routes through the
// shared Context application service (Platform.Analyze) — the same service the
// CLI `kern analyze` fast path uses — so no interface inlines the context
// workflow (P2 exit gate: no core business workflow exists only in one
// interface). POST only.
func (a *App) handleV1Context(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	a.freshGraph()
	pkt, _, err := a.platform.Analyze(req.Change)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pkt)
}

// handleV1Risk analyzes a proposed change and returns the risk assessment
// extracted from the ContextPacket. POST only.
func (a *App) handleV1Risk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	a.freshGraph()
	pkt, _, err := a.taskSvc.Risk(req.Change)
	if err != nil {
		if st := symbolErrorStatus(err); st != 0 {
			writeError(w, st, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"risks": pkt.Risks, "change": req.Change})
}

// handleV1Task serves a single task by ID (GET /v1/tasks/{id}) and dispatches
// the nested task-action aliases (POST /v1/tasks/{id}/{action}: analyze, plan,
// approve, execute, verify, deploy, artifacts). The registry is backed by a
// persisted TaskStore (wired in New), so submitted tasks are served here; a
// lookup falls back to the store for tasks persisted across restarts and
// returns 404 only when a task is genuinely unknown.
func (a *App) handleV1Task(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	segments := strings.SplitN(raw, "/", 2)
	id, err := url.PathUnescape(segments[0])
	if err != nil || strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	var action string
	if len(segments) > 1 {
		if action, err = url.PathUnescape(segments[1]); err != nil || action == "" {
			writeError(w, http.StatusBadRequest, "invalid task action")
			return
		}
	}
	// Nested task-action alias: /v1/tasks/{id}/{action}
	if action != "" {
		a.handleV1TaskAction(w, r, id, action)
		return
	}
	// Plain GET /v1/tasks/{id} → task detail.
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	task, ok := a.tasks.Get(id)
	if !ok {
		if st := a.tasks.TaskStore(); st != nil {
			if t, serr := st.Get(id); serr == nil {
				writeJSON(w, http.StatusOK, t)
				return
			}
		}
		writeError(w, http.StatusNotFound, "task not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleV1TaskAction dispatches the nested task-action aliases under
// /v1/tasks/{id}/{action}. These are ADDITIONAL routes that delegate to the
// existing top-level handlers, preserving backward compatibility while
// exposing the spec's nested action paths. The task id is threaded through the
// request body (as "task_id", or as "id" for the approval action).
func (a *App) handleV1TaskAction(w http.ResponseWriter, r *http.Request, taskID, action string) {
	switch action {
	case "analyze":
		a.handleV1Analyze(w, injectBody(r, map[string]any{"task_id": taskID}))
	case "plan":
		a.handleV1Plan(w, injectBody(r, map[string]any{"task_id": taskID}))
	case "approve":
		a.handleApprovalApprove(w, injectBody(r, map[string]any{"id": taskID}))
	case "execute":
		a.handleV1Execute(w, injectBody(r, map[string]any{"task_id": taskID}))
	case "verify":
		a.handleV1Verify(w, injectBody(r, map[string]any{"task_id": taskID}))
	case "deploy":
		a.handleV1Deploy(w, r, taskID)
	case "artifacts":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		arts, err := app.NewArtifactStore(a.root).GetByTask(taskID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, arts)
	default:
		writeError(w, http.StatusNotFound, "unknown task action: "+action)
	}
}

// handleV1Deploy deploys a task through the task-action alias
// (POST /v1/tasks/{id}/deploy). Routes through TaskService.Deploy so the
// governance firewall, approval gate, and lifecycle events all apply. The
// task id is threaded directly (not via injectBody) because Deploy takes it
// as an argument, and the body carries only the optional version. Returns the
// updated task on success; 404 for unknown tasks, 403 when a human approval
// is pending (the approval id is embedded in the error message), and 500 for
// any other failure.
//
// Governance gate: production mutation is disabled by default. The same
// KERN_ALLOW_DEPLOY=1 gate the CLI/loop paths enforce is applied HERE, before
// the deployer is invoked, REGARDLESS of deployer type — so the web path can
// never bypass the governance firewall by running with the default
// NoopDeployer while the CLI path is gated. When the gate is off the handler
// returns 403 with the opt-in guidance and does not touch the task.
func (a *App) handleV1Deploy(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if os.Getenv("KERN_ALLOW_DEPLOY") != "1" {
		writeError(w, http.StatusForbidden,
			"deployment refused: KERN_ALLOW_DEPLOY not set (production mutation disabled by default); set KERN_ALLOW_DEPLOY=1 to enable deploys via the web console")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	t, err := a.taskSvc.Deploy(taskID, req.Version)
	if err != nil {
		if errors.Is(err, agent.ErrApprovalRequired) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, agent.ErrInvalidTransition) {
			// The task is not in a deployable state (e.g. freshly created,
			// still running, or already terminal). 409 tells the caller the
			// task exists but cannot be deployed in its current state.
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "task not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// injectBody returns a shallow copy of r whose JSON body is the decoded request
// object merged with the given key/value pairs. It lets the nested task-action
// routes append the task id onto the body before delegating to an existing
// handler without changing the top-level behavior. A non-object/empty body is
// treated as an empty object.
func injectBody(r *http.Request, extra map[string]any) *http.Request {
	var obj map[string]any
	_ = json.NewDecoder(r.Body).Decode(&obj)
	if obj == nil {
		obj = map[string]any{}
	}
	for k, v := range extra {
		obj[k] = v
	}
	b, _ := json.Marshal(obj)
	nr := r.Clone(r.Context())
	nr.Body = io.NopCloser(bytes.NewReader(b))
	nr.ContentLength = int64(len(b))
	return nr
}

// handleV1Incidents serves the incident list (GET /v1/incidents) — the A9
// route the Python SDK's incidents() method calls. It mirrors the
// dashboard's /api/incidents shape ({"items": [...]}).
func (a *App) handleV1Incidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := a.buildIncidents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleV1Incident serves a single incident by ID (GET /v1/incidents/{id}). The
// reserved "investigate" segment is delegated to handleV1IncidentInvestigate so
// both routes coexist.
func (a *App) handleV1Incident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/incidents/"))
	if err != nil || strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	if id == "investigate" {
		a.handleV1IncidentInvestigate(w, r)
		return
	}
	inc, err := a.inter.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "incident not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

// v1TaskResponse is the lowercase JSON projection for task submission results.
type v1TaskResponse struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Type  string `json:"type"`
}

// handleV1TaskSubmit submits a new task to the agent registry (POST /v1/tasks)
// and returns its id and initial state.
func (a *App) handleV1TaskSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Input string `json:"input"`
		Type  string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		req.Type = "code"
	}
	tk := agent.NewTask(req.Type, req.Input)
	if a.tasks.TaskStore() != nil {
		// The persisted store owns task IDs (cross-process unique under its
		// file lock); see agent.Registry.SubmitTask.
		tk.ID = ""
	}
	if err := a.tasks.SubmitTask(tk); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v1TaskResponse{ID: tk.ID, State: string(tk.State), Type: tk.Type})
}

// v1Specialist is the lowercase JSON projection of a standard-team specialist.
type v1Specialist struct {
	ID           string   `json:"id"`
	Role         string   `json:"role"`
	Capabilities []string `json:"capabilities"`
}

// handleV1Agents builds the standard specialist team and returns the roster
// plus the current task states from the agent registry (POST /v1/agents).
// It is read-only.
func (a *App) handleV1Agents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, reg, err := agents.StandardTeam()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	specialists := make([]v1Specialist, 0)
	for _, ag := range reg.All() {
		caps := append([]string{}, ag.Capabilities...)
		specialists = append(specialists, v1Specialist{ID: ag.ID, Role: ag.Type, Capabilities: caps})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"specialists": specialists,
		"tasks":       a.tasks.ListTasks(),
	})
}

// v1LoopStage is the lowercase JSON projection of one loop stage result.
type v1LoopStage struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
	Output string `json:"output"`
}

// v1LoopResponse is the lowercase JSON projection of a closed-loop run.
type v1LoopResponse struct {
	Intent          string        `json:"intent"`
	Level           string        `json:"level"`
	RequestedLevel  string        `json:"requested_level,omitempty"`
	Capped          bool          `json:"capped,omitempty"`
	Stages          []v1LoopStage `json:"stages"`
	Deployed        bool          `json:"deployed"`
	ObservedHealthy bool          `json:"observed_healthy"`
	Learned         string        `json:"learned,omitempty"`
}

// handleV1Loop runs the closed loop against an intent and returns the stage
// timeline plus the outcome (POST /v1/loop). The autonomy level defaults to
// L0 (read-only); AI stages use the deterministic no-op default.
//
// Autonomy cap: the client's requested level is capped at the server's
// configured maximum (KERN_WEB_MAX_AUTONOMY, default L0 — the server's own
// autonomy). A request above the cap runs AT the cap, never above it, so an
// unconfigured console cannot be escalated to write levels by a client.
//
// Deadline: the loop derives its context from the request (a client
// disconnect cancels the run between stages) wrapped in a server-side timeout
// (KERN_WEB_LOOP_TIMEOUT, default 30m) so a wedged run cannot hang the
// handler. On timeout the handler returns 504 and the run is CANCELLED
// between stages — the aborted task is marked FAILED and remains observable,
// instead of the run continuing in the background (oracle-gate ctx
// threading).
//
// Latency note: an L0 run is NOT instant. The verify stage always runs the
// full verification engine (go build + go test + security + architecture +
// dependency checks) against a sandbox worktree, which takes ~15-30s on a
// real repository. This is inherent closed-loop semantics, identical to the
// CLI's `kern loop`; there is intentionally no "skip verify" parameter. At
// L0/L1 the verify result is advisory — a FAIL reflects pre-existing repo
// hygiene (surfaced as VerifyAdvisory), never the run's own change surface,
// and never fails the run. Clients must allow >30s for this endpoint.
func (a *App) handleV1Loop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Intent string `json:"intent"`
		Level  string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Intent) == "" {
		writeError(w, http.StatusBadRequest, "intent is required")
		return
	}
	level := loop.L0
	if req.Level != "" {
		parsed, err := loop.ParseLevel(req.Level)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		level = parsed
	}
	// Server-side autonomy cap: a client request above the configured cap runs
	// AT the cap (never above it). The cap defaults to the server's own
	// autonomy (L0, read-only), so an unconfigured console cannot be escalated
	// by a client-supplied level. The response tells the client both the
	// requested and the effective level (gate-3).
	requestedLevel := level
	capped := false
	if cap := maxLoopAutonomy(); level > cap {
		level = cap
		capped = true
	}

	// Server-side deadline + request cancellation. RunLoopContext threads the
	// request-derived context into the loop, so the first of completion, the
	// KERN_WEB_LOOP_TIMEOUT deadline, or a client disconnect CANCELS the run
	// between stages (the run no longer continues in the background — its
	// progress is not silently abandoned, and the aborted Task is marked
	// FAILED so it stays observable and auditable).
	ctx, cancel := context.WithTimeout(r.Context(), loopTimeout())
	defer cancel()
	type loopOutcome struct {
		res *loop.Result
		err error
	}
	done := make(chan loopOutcome, 1)
	go func() {
		_, res, err := a.taskSvc.RunLoopContext(ctx, req.Intent, level)
		done <- loopOutcome{res: res, err: err}
	}()

	select {
	case <-ctx.Done():
		// The request context ended before the loop did: either the
		// server-side deadline fired or the client disconnected. A deadline
		// gets an explicit 504 naming the timeout; a disconnect has no one to
		// write to, so the cancelled run's result is discarded (the loop
		// stopped between stages and the aborted Task is marked FAILED on the
		// task registry).
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Deliberately bypass writeError's 5xx masking: this message is
			// client-facing guidance (it names a public env var), not an
			// internal detail.
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error": fmt.Sprintf("loop timed out after %s (KERN_WEB_LOOP_TIMEOUT); the run was cancelled between stages and the task is marked FAILED", loopTimeout()),
			})
		}
		return
	case out := <-done:
		if out.err != nil {
			writeError(w, http.StatusInternalServerError, out.err.Error())
			return
		}
		res := out.res
		if res == nil {
			writeError(w, http.StatusInternalServerError, "loop returned no result")
			return
		}
		response := v1LoopResponse{
			Intent:          res.Intent,
			Level:           res.Level.String(),
			RequestedLevel:  requestedLevel.String(),
			Capped:          capped,
			Deployed:        res.Deployed,
			ObservedHealthy: res.ObservedHealthy,
		}
		for _, st := range res.Stages {
			response.Stages = append(response.Stages, v1LoopStage{Stage: st.Stage, Status: st.Status, Output: st.Output})
		}
		if res.Learned != nil {
			response.Learned = res.Learned.ID
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// loopTimeoutEnv is the environment variable controlling the server-side
// /v1/loop deadline. loopTimeoutEnvName is its human-readable name for
// messages.
const (
	loopTimeoutEnv     = "KERN_WEB_LOOP_TIMEOUT"
	defaultLoopTimeout = 30 * time.Minute
)

// loopTimeout returns the server-side /v1/loop deadline: KERN_WEB_LOOP_TIMEOUT
// when set to a positive duration, else defaultLoopTimeout (30m). An
// unparsable or non-positive value logs a warning and falls back to the
// default rather than failing the endpoint.
func loopTimeout() time.Duration {
	if v := os.Getenv(loopTimeoutEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Printf("WARNING: invalid %s %q; using default %s", loopTimeoutEnv, v, defaultLoopTimeout)
	}
	return defaultLoopTimeout
}

// maxLoopAutonomyEnv is the environment variable capping the autonomy a
// client may request on /v1/loop.
const maxLoopAutonomyEnv = "KERN_WEB_MAX_AUTONOMY"

// maxLoopAutonomy returns the server-side /v1/loop autonomy cap: the
// KERN_WEB_MAX_AUTONOMY level when set and valid, else the server's default
// autonomy (L0 — read-only). An unparsable value logs a warning and falls
// back to L0. A client request above the cap runs AT the cap.
func maxLoopAutonomy() loop.Autonomy {
	if v := os.Getenv(maxLoopAutonomyEnv); v != "" {
		if lvl, err := loop.ParseLevel(v); err == nil {
			return lvl
		}
		log.Printf("WARNING: invalid %s %q; using default L0", maxLoopAutonomyEnv, v)
	}
	return loop.L0
}

// handleV1IncidentInvestigate runs the full incident workflow against an alert
// through the shared Incident application service (TaskService.InvestigateIncident)
// — the same service MCP kern_incident and CLI `kern incident` use — so the
// lifecycle (IngestAlert → Correlate → RootCause) creates an authoritative Task
// with incident + root-cause artifacts and no interface inlines the workflow
// (P2 exit gate). Returns the incident, its hypotheses and the affected service
// (POST /v1/incidents/investigate).
func (a *App) handleV1IncidentInvestigate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Alert domain.Alert `json:"alert"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Alert.Message == "" {
		writeError(w, http.StatusBadRequest, "alert with a message is required")
		return
	}
	_, inc, _, err := a.taskSvc.InvestigateIncident(req.Alert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"incident":         inc,
		"hypotheses":       inc.Hypotheses,
		"affected_service": inc.AffectedService,
	})
}

// handleV1ArtifactsList lists artifacts, optionally filtered by task ID via
// ?task=<id>. GET only.
func (a *App) handleV1ArtifactsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store := app.NewArtifactStore(a.root)
	taskID := r.URL.Query().Get("task")
	if taskID != "" {
		arts, err := store.GetByTask(taskID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, arts)
		return
	}
	arts, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

// handleV1ArtifactGet serves a single artifact by ID. GET only.
func (a *App) handleV1ArtifactGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/artifacts/")
	id, err := url.PathUnescape(id)
	if err != nil || strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "invalid artifact id")
		return
	}
	store := app.NewArtifactStore(a.root)
	art, err := store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, art)
}

// handleV1Correlate correlates a production alert against the runtime and
// returns the deep evidence chain. .
func (a *App) handleV1Correlate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Alert    domain.Alert `json:"alert"`
		Snapshot string       `json:"snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Snapshot != "" {
		store, err := runtime.ParseSnapshot([]byte(req.Snapshot))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid snapshot: "+err.Error())
			return
		}
		a.platform.WithRuntimeSource(store)
	}
	t, chain, _, err := a.taskSvc.Correlate(req.Alert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Chain  runtime.CorrelationChain `json:"chain"`
		TaskID string                   `json:"task_id,omitempty"`
	}{Chain: chain, TaskID: t.ID})
}

// handleV1Learn extracts recurring patterns from engineering memory. .
func (a *App) handleV1Learn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Threshold int `json:"threshold"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	t, patterns, _, err := a.taskSvc.Learn(req.Threshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Patterns []learning.Pattern `json:"patterns"`
		TaskID   string             `json:"task_id,omitempty"`
	}{Patterns: patterns, TaskID: t.ID})
}

// handleV1Modernize runs the legacy modernization analysis.
//
// Latency note: the analyzer walks the whole prebuilt index (communities →
// bridges → churn → candidate boundaries → impact → risk → migration plan),
// which takes ~20-30s on a real repository. This is inherent analysis cost;
// clients must allow >30s for this endpoint.
func (a *App) handleV1Modernize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, plan, _, err := a.taskSvc.Modernize()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Plan   modernization.ExtractionPlan `json:"plan"`
		TaskID string                       `json:"task_id,omitempty"`
	}{Plan: plan, TaskID: t.ID})
}

// handleV1Execute applies a patch in a sandboxed worktree via TaskService.Execute.
// /22: governance-gated, creates an authoritative Task with a diff artifact.
func (a *App) handleV1Execute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Patch string `json:"patch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Patch) == "" {
		writeError(w, http.StatusBadRequest, "patch is required")
		return
	}
	t, diff, err := a.taskSvc.Execute(req.Patch)
	if err != nil {
		if errors.Is(err, app.ErrInvalidPatch) {
			// A malformed / non-applicable patch is a CLIENT error: the caller
			// sent a patch that cannot be applied, so it must not surface as a
			// 500 "internal error". Keep the message (it names the git apply
			// failure) so the client can see why the patch was rejected.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Diff   string `json:"diff"`
		TaskID string `json:"task_id,omitempty"`
		Output string `json:"output,omitempty"`
	}{Diff: diff, TaskID: t.ID, Output: t.Output})
}

// handleV1Audit returns the audit trail for a task: its steps, artifacts,
// governance audit entries, and pending approvals. Invariant 4: every
// important task action is auditable. The spec requires GET /v1/audit/{task_id}.
func (a *App) handleV1Audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	taskID := strings.TrimPrefix(r.URL.Path, "/v1/audit/")
	taskID, err := url.PathUnescape(taskID)
	if err != nil || strings.TrimSpace(taskID) == "" {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	t, ok := a.taskSvc.Get(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found: "+taskID)
		return
	}
	// Gather the task's artifacts from the artifact store.
	var arts []domain.Artifact
	if a.taskSvc.Artifacts() != nil {
		if list, err := a.taskSvc.Artifacts().GetByTask(taskID); err == nil {
			arts = list
		}
	}
	// Gather governance audit entries for this task (Invariant 4). The
	// governance package reads the tamper-evident audit trail so the console
	// and the CLI/MCP surface the same records.
	var auditEntries []governance.AuditEntry
	if entries, err := governance.ReadAuditTrail(r.Context(), a.root, taskID); err == nil {
		auditEntries = entries
	}
	// Gather pending approvals for this task from the persistent approval
	// store the App already owns (a.fileApprovals).
	var pendingApprovals []domain.Approval
	if pending, err := a.fileApprovals.Pending(); err == nil {
		for _, ap := range pending {
			if ap.TaskID == taskID {
				pendingApprovals = append(pendingApprovals, ap)
			}
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Task      *agent.Task             `json:"task"`
		Artifacts []domain.Artifact       `json:"artifacts"`
		Audit     []governance.AuditEntry `json:"audit"`
		Approvals []domain.Approval       `json:"approvals"`
	}{
		Task:      t,
		Artifacts: arts,
		Audit:     auditEntries,
		Approvals: pendingApprovals,
	})
}

// handleV1ToolCall is the single REST passthrough to the full MCP tool
// catalog: POST /v1/tools/{name} delegates to the same in-process governed
// dispatch path MCP clients hit (KERN_TOOLS allowlist, root confinement,
// RBAC), so non-Go SDKs reach the entire catalog through ONE route instead of
// 140 typed endpoints. The request body is the tool's argument map; the
// response is {"output": <raw tool text>}. The governance posture is
// identical to the other /v1 routes — ServeHTTP's bearer gate, rate limit and
// CSRF/DNS-rebinding guard all apply — and the in-process call itself fails
// closed exactly like an MCP tools/call: out-of-confinement roots, allowlist
// denials and RBAC refusals surface as 403, unknown tools as 404. The call
// runs under the same per-call ceiling MCP imposes (30m) so a slow tool
// cannot hang the handler.
func (a *App) handleV1ToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/tools/"))
	if err != nil || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "invalid tool name")
		return
	}
	var args map[string]any
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if a.tools == nil {
		writeError(w, http.StatusServiceUnavailable, "tool dispatch unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	out, err := a.tools.CallToolGoverned(ctx, name, args)
	if err != nil {
		// Typed sentinel mapping (finding 6): the governed dispatch path
		// classifies every pre-execution denial as domain.ErrToolDenied and
		// every unknown tool as domain.ErrToolUnknown, so the HTTP status is
		// decided by errors.Is, never by matching raw error text. Only the
		// generic safe messages are returned to the client; the full detail
		// is logged server-side (a raw err.Error() leak discloses internal
		// paths/policy to REST clients).
		switch {
		case errors.Is(err, domain.ErrToolUnknown):
			writeError(w, http.StatusNotFound, "unknown tool: "+name)
		case errors.Is(err, domain.ErrToolDenied):
			writeError(w, http.StatusForbidden, "tool call denied")
		default:
			log.Printf("web: /v1/tools/%s failed: %v", name, err)
			writeError(w, http.StatusInternalServerError, "tool call failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out})
}

// handleV1EventsStream provides a JSON-lines Server-Sent Events (SSE) stream for real-time telemetry (KernOps Phase 2).
func (a *App) handleV1EventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Standard SSE hardening: ask any buffering reverse proxy / accelerator
	// (nginx X-Accel, CDNs) not to buffer the stream, so the initial chunk
	// reaches the client immediately instead of after the handler returns.
	w.Header().Set("X-Accel-Buffering", "no")

	if a.bus == nil {
		if _, err := fmt.Fprintf(w, "event: error\ndata: {\"error\":\"event bus not configured\"}\n\n"); err != nil {
			log.Printf("sse: write failed: %v", err)
			return
		}
		flusher.Flush()
		return
	}

	// Write + flush the initial "connected" chunk BEFORE subscribing so the
	// client always observes the handshake first — a client parser that
	// expects "connected" before any bus event must never see a data event
	// arrive ahead of it — and so the chunk is on the wire immediately.
	if _, err := fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\",\"timestamp\":%d}\n\n", time.Now().Unix()); err != nil {
		log.Printf("sse: write failed: %v", err)
		return
	}
	flusher.Flush()

	ch := make(chan eventbus.Event, 64)
	unsub := a.bus.Subscribe("", func(ev eventbus.Event) {
		select {
		case ch <- ev:
		default:
			// Dropped if client is slow
		}
	})
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, string(data)); err != nil {
				// The client disconnected (or the write otherwise failed).
				// The request context cancellation also triggers ctx.Done()
				// below, but a failing write is the definitive signal — stop
				// the stream cleanly instead of looping on broken writes.
				log.Printf("sse: write failed: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}

// handleRuntimeJSON serves the production-intelligence snapshot: which
// runtime source is wired (status: source, poll interval, per-service
// profiles) plus the code-vs-runtime route drift. Same shapes as
// `kern runtime status|drift --json` (shared runtime.StatusSnapshot /
// DriftSnapshot builders), so the CLI, MCP, and web surfaces cannot drift.
func (a *App) handleRuntimeJSON(w http.ResponseWriter, r *http.Request) {
	src := runtime.LoadSource(a.root)
	snap := runtime.StatusSnapshot(src)
	_, ix := a.freshGraph()
	var codeRoutes []string
	if ix != nil {
		for _, sym := range ix.Symbols {
			if sym.Route != "" {
				codeRoutes = append(codeRoutes, sym.Route)
			}
		}
	}
	snap["drift"] = runtime.DriftSnapshot(src, codeRoutes)
	writeJSON(w, http.StatusOK, snap)
}
