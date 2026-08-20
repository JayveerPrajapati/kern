package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// v1AnalyzeResponse wraps the structured context packet with its rendered
// human-readable text so callers can consume either form.
type v1AnalyzeResponse struct {
	Packet domain.ContextPacket `json:"packet"`
	Text   string               `json:"text"`
}

// handleV1Analyze analyzes a proposed change via the context engine and
// returns the assembled ContextPacket plus a rendered text summary. POST only.
func (a *App) handleV1Analyze(w http.ResponseWriter, r *http.Request) {
	a.handleV1Change(w, r)
}

// handleGovernanceMetrics aggregates AI-governance metrics from the
// subsystems currently wired onto the App and returns them as a metrics.Snapshot
// with the governance fields populated. It starts from the live process-wide
// recorder snapshot (so performance/self-observability counts ride along) and
// overrides only the governance fields. Sources that are not wired on this App
// contribute 0 rather than forcing new dependencies:
//   - AgentCount: registered agents from agent.Registry
//   - TaskCount: tasks tracked by the agent registry
//   - BlocksCount: audit entries with a blocked/denied decision
//   - OverridesCount: audit entries with a human "approved" decision (override)
//   - ViolationsCount: cached architecture validation report violations
//   - AvgConfidence: no claim store is exposed on the App; 0 until one is wired
func (a *App) handleGovernanceMetrics(w http.ResponseWriter, r *http.Request) {
	snap := metrics.Default().Snapshot()

	if a.tasks != nil {
		snap.AgentCount = len(a.tasks.All())
		snap.TaskCount = a.tasks.TaskCount()
	}

	if a.firewall != nil {
		for _, e := range a.firewall.AuditLog().All() {
			switch e.Result {
			case "blocked", "denied":
				snap.BlocksCount++
			case "approved":
				snap.OverridesCount++
			}
		}
	}

	if rep, err := a.buildArchitecture(); err == nil && rep != nil {
		snap.ViolationsCount = len(rep.Violations)
	}

	writeJSON(w, http.StatusOK, snap)
}

// handleV1Plan is an alias of the analyze workflow (same deterministic core).
func (a *App) handleV1Plan(w http.ResponseWriter, r *http.Request) {
	a.handleV1Change(w, r)
}

// handleV1Change is the shared implementation for /v1/analyze and /v1/plan.
func (a *App) handleV1Change(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Refresh the shared graph/index before analyzing so /v1/analyze reports
	// current state instead of the startup snapshot after a file edit.
	a.freshGraph()
	var req struct {
		Change string `json:"change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Change) == "" {
		writeError(w, http.StatusBadRequest, "change is required")
		return
	}
	pkt, err := a.ctx.AnalyzeChange(req.Change)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v1AnalyzeResponse{Packet: pkt, Text: context.RenderText(pkt)})
}

// handleV1WhatIf simulates a hypothetical change to the knowledge graph and
// returns the deterministic impact report. Accepts an optional kind
// (remove_symbol | change_dependency, default remove_symbol) and new_target.
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
	g, _ := a.freshGraph()
	imp := whatif.Simulate(g, whatif.Change{Kind: kind, Target: req.Change, NewTarget: req.NewTarget})
	writeJSON(w, http.StatusOK, imp)
}

// handleV1Impact is an alias of the what-if handler (same impact report).
func (a *App) handleV1Impact(w http.ResponseWriter, r *http.Request) {
	a.handleV1WhatIf(w, r)
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
	res := a.ver.Verify(types)
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
	for i := range g.Nodes {
		if g.Nodes[i].ID == entity {
			node = &g.Nodes[i]
			break
		}
	}
	if node == nil {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"node":         *node,
		"who_calls":    g.WhoCalls(entity),
		"depends_on":   g.WhatDependsOn(entity),
		"depends_upon": g.WhatDoesXDependOn(entity),
	})
}

// handleV1Context analyzes a proposed change and returns the raw ContextPacket
// (the "context" view) with no rendered summary wrapper. POST only.
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
	pkt, err := a.ctx.AnalyzeChange(req.Change)
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
	pkt, err := a.ctx.AnalyzeChange(req.Change)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"risks": pkt.Risks, "change": req.Change})
}

// handleV1Task serves a single task by ID (GET). The registry is backed by a
// persisted TaskStore (wired in New), so submitted tasks are served here; a
// lookup falls back to the store for tasks persisted across restarts and
// returns 404 only when a task is genuinely unknown.
func (a *App) handleV1Task(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"))
	if err != nil || strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "invalid task id")
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
	writeJSON(w, http.StatusOK, map[string]interface{}{
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
	Stages          []v1LoopStage `json:"stages"`
	Deployed        bool          `json:"deployed"`
	ObservedHealthy bool          `json:"observed_healthy"`
	Learned         string        `json:"learned,omitempty"`
}

// handleV1Loop runs the closed loop against an intent and returns the stage
// timeline plus the outcome (POST /v1/loop). The autonomy level defaults to
// L0 (read-only); AI stages use the deterministic no-op default.
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
	l, err := loop.NewLoop(loop.LoopConfig{Root: a.root, Level: level, Mem: a.memories, Recorder: flight.New(a.root)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := l.Run(req.Intent, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := v1LoopResponse{
		Intent:          res.Intent,
		Level:           res.Level.String(),
		Deployed:        res.Deployed,
		ObservedHealthy: res.ObservedHealthy,
	}
	for _, st := range res.Stages {
		out.Stages = append(out.Stages, v1LoopStage{Stage: st.Stage, Status: st.Status, Output: st.Output})
	}
	if res.Learned != nil {
		out.Learned = res.Learned.ID
	}
	writeJSON(w, http.StatusOK, out)
}

// handleV1IncidentInvestigate runs the incident engine against an alert —
// IngestAlert + Correlate + RootCause — and returns the resulting incident, its
// hypotheses and the affected service (POST /v1/incidents/investigate). A
// runtime source is wired so correlation can reason over production telemetry.
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
	eng := a.inc
	inc := eng.IngestAlert(req.Alert)
	eng.Correlate(inc)
	eng.RootCause(inc)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"incident":         inc,
		"hypotheses":       inc.Hypotheses,
		"affected_service": inc.AffectedService,
	})
}
