package web

import (
	"net/http"
	"sort"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/governance/risk"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// ---------------------------------------------------------------------------
// Risks
// ---------------------------------------------------------------------------

// riskItem is the JSON/template projection of a deterministic risk assessment
// for a single resource+action pair.
type riskItem struct {
	Resource         string  `json:"resource"`
	Action           string  `json:"action"`
	Level            string  `json:"level"`
	Score            float64 `json:"score"`
	ApprovalRequired bool    `json:"approval_required"`
	Blocked          bool    `json:"blocked"`
	Mitigation       string  `json:"mitigation"`
}

// riskPairs is the standard set of resource+action combinations assessed by the
// /risks page. It is derived from the default governance policy rules and is
// deterministic (no live LLM).
var riskPairs = []struct{ res, act string }{
	{"source", "write"},
	{"documentation", "write"},
	{"security", "write"},
	{"production", "deploy"},
	{"database", "drop"},
	{"tests", "write"},
	{"config", "write"},
}

// buildRisks computes a deterministic risk assessment for each standard
// resource+action pair via the shared governance/risk assessor.
func (a *App) buildRisks() []riskItem {
	assessor := risk.NewRiskAssessor(governance.DefaultPolicies())
	out := make([]riskItem, 0, len(riskPairs))
	for _, p := range riskPairs {
		r := assessor.AssessAction(p.res, p.act)
		out = append(out, riskItem{
			Resource:         p.res,
			Action:           p.act,
			Level:            string(r.Level),
			Score:            r.Score,
			ApprovalRequired: r.ApprovalRequired,
			Blocked:          r.Blocked,
			Mitigation:       r.Mitigation,
		})
	}
	return out
}

// risksData is the template model for the /risks HTML page.
type risksData struct {
	Root  string
	Risks []riskItem
}

func (a *App) handleRisks(w http.ResponseWriter, r *http.Request) {
	data := risksData{Root: a.root, Risks: a.buildRisks()}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.risksT.Execute(w, data)
}

func (a *App) handleRisksJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": a.buildRisks()})
}

// ---------------------------------------------------------------------------
// Artifacts
// ---------------------------------------------------------------------------

// artifactItem is the JSON/template projection of a domain.Artifact.
type artifactItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Type      string `json:"type"`
	TaskID    string `json:"task_id"`
	CreatedBy string `json:"created_by"`
	Status    string `json:"status"`
	URI       string `json:"uri"`
}

// buildArtifacts lists every persisted artifact via the shared ArtifactStore.
func (a *App) buildArtifacts() []artifactItem {
	arts, err := app.NewArtifactStore(a.root).List()
	if err != nil {
		return []artifactItem{}
	}
	out := make([]artifactItem, 0, len(arts))
	for _, art := range arts {
		out = append(out, artifactItem{
			ID:        art.ID,
			Kind:      string(art.Kind),
			Type:      art.Type,
			TaskID:    art.TaskID,
			CreatedBy: art.CreatedBy,
			Status:    art.Status,
			URI:       art.URI,
		})
	}
	return out
}

// artifactsData is the template model for the /artifacts HTML page.
type artifactsData struct {
	Root      string
	Artifacts []artifactItem
	Count     int
}

func (a *App) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	arts := a.buildArtifacts()
	data := artifactsData{Root: a.root, Artifacts: arts, Count: len(arts)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.artifactsT.Execute(w, data)
}

func (a *App) handleArtifactsJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": a.buildArtifacts()})
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// buildAudit projects the full governance audit trail (firewall audit log).
func (a *App) buildAudit() []governanceAuditEntry {
	if a.firewall == nil {
		return []governanceAuditEntry{}
	}
	entries := a.firewall.AuditLog().All()
	out := make([]governanceAuditEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, governanceAuditEntry{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			AgentID:   e.AgentID,
			Action:    e.Action,
			Resource:  e.Resource,
			Approved:  e.Approved,
			Result:    e.Result,
		})
	}
	return out
}

// auditData is the template model for the /audit HTML page.
type auditData struct {
	Root  string
	Audit []governanceAuditEntry
	Count int
}

func (a *App) handleAudit(w http.ResponseWriter, r *http.Request) {
	audit := a.buildAudit()
	data := auditData{Root: a.root, Audit: audit, Count: len(audit)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.auditT.Execute(w, data)
}

func (a *App) handleAuditJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": a.buildAudit()})
}

// ---------------------------------------------------------------------------
// System Map (architecture overview)
// ---------------------------------------------------------------------------

// systemModule is a projection of one indexed package/module in the repo.
type systemModule struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Lang  string `json:"lang"`
	Files int    `json:"files"`
}

// systemMapData is the template/JSON model for the /system-map page.
type systemMapData struct {
	Root        string         `json:"root"`
	Modules     []systemModule `json:"modules"`
	ModuleCount int            `json:"module_count"`
	FileCount   int            `json:"file_count"`
	EdgeCount   int            `json:"edge_count"`
	SymbolCount int            `json:"symbol_count"`
	Communities int            `json:"communities"`
	Hubs        int            `json:"hubs"`
}

// buildSystemMap assembles an architecture overview from the prebuilt index and
// graph (never re-running index.Build per request).
func (a *App) buildSystemMap() *systemMapData {
	g, ix := a.freshGraph()
	modules := make([]systemModule, 0, len(ix.Pkgs))
	fileSet := map[string]bool{}
	for _, p := range ix.Pkgs {
		for _, f := range p.Files {
			fileSet[f] = true
		}
		modules = append(modules, systemModule{
			Name:  p.Name,
			Path:  p.Path,
			Lang:  p.Lang,
			Files: len(p.Files),
		})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return &systemMapData{
		Root:        a.root,
		Modules:     modules,
		ModuleCount: len(modules),
		FileCount:   len(fileSet),
		EdgeCount:   len(g.Edges),
		SymbolCount: len(ix.Symbols),
		Communities: len(intel.Communities(ix)),
		Hubs:        len(intel.Hubs(ix, 5)),
	}
}

func (a *App) handleSystemMap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.systemMapT.Execute(w, a.buildSystemMap())
}

func (a *App) handleSystemMapJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildSystemMap())
}

// ---------------------------------------------------------------------------
// Incidents
// ---------------------------------------------------------------------------

// incidentsData is the template model for the /incidents HTML page.
type incidentsData struct {
	Root      string
	Incidents []incidentSummary
	Count     int
}

func (a *App) handleIncidentsPage(w http.ResponseWriter, r *http.Request) {
	items, err := a.buildIncidents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := incidentsData{Root: a.root, Incidents: items, Count: len(items)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.incidentsT.Execute(w, data)
}

// ---------------------------------------------------------------------------
// Efficiency
// ---------------------------------------------------------------------------

func (a *App) handleEfficiency(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.efficiencyT.Execute(w, data)
}

func (a *App) handleEfficiencyJSON(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// ---------------------------------------------------------------------------
// Approvals
// ---------------------------------------------------------------------------

// approvalsData is the template model for the /approvals HTML page.
type approvalsData struct {
	Root      string
	Approvals []domainApproval
	Count     int
}

func (a *App) handleApprovals(w http.ResponseWriter, r *http.Request) {
	pending := a.buildApprovals()
	data := approvalsData{Root: a.root, Approvals: pending, Count: len(pending)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.approvalsT.Execute(w, data)
}

// ---------------------------------------------------------------------------
// Graph / Memory / Architecture / Eval (Phase 18)
// ---------------------------------------------------------------------------

// graphPageData wraps graphData with the project root for the /graph HTML page.
// graphData itself stays JSON-shaped (it backs /api/graph); this wrapper is
// template-only so the API payload is unchanged.
type graphPageData struct {
	Root string
	graphData
}

// memoryPageData wraps memoryData with the project root for the /memory HTML
// page. memoryData stays JSON-shaped (it backs /api/memory).
type memoryPageData struct {
	Root string
	memoryData
}

// architecturePageData wraps the architecture report with the project root for
// the /architecture HTML page. architectureData stays JSON-shaped (it backs
// /api/architecture).
type architecturePageData struct {
	Root string
	*architectureData
}

// handleGraphPage serves the HTML graph inspector at /graph. It reuses
// buildGraph to render the top hubs (nodes) and discovered communities
// (clusters) as inspectable lists, with a link to the /api/graph JSON endpoint.
func (a *App) handleGraphPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.graphT.Execute(w, graphPageData{Root: a.root, graphData: a.buildGraph(50)})
}

// handleMemoryPage serves the HTML memory roster at /memory. It reuses
// buildMemory to render every typed memory with its content, scope, type and
// freshness (superseded) state.
func (a *App) handleMemoryPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.memoryT.Execute(w, memoryPageData{Root: a.root, memoryData: a.buildMemory()})
}

// handleArchitecturePage serves the HTML architecture report at /architecture.
// It reuses buildArchitecture to render the validation summary and any layered-
// rule violations, with a link to the /api/architecture JSON endpoint.
func (a *App) handleArchitecturePage(w http.ResponseWriter, r *http.Request) {
	rep, err := a.buildArchitecture()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.architectureT.Execute(w, architecturePageData{Root: a.root, architectureData: rep})
}

// handleEvalPage serves the HTML evaluation page at /eval. It reuses buildEval
// (which in turn reuses buildAgents and buildTasks) to render three stacked
// inspectors: agent comparison, task replay and context inspection.
func (a *App) handleEvalPage(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildEval()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.evalT.Execute(w, data)
}
