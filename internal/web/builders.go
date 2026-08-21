package web

import (
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/architecture"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

type overviewData struct {
	Root               string `json:"root"`
	Symbols            int    `json:"symbols"`
	Files              int    `json:"files"`
	Edges              int    `json:"edges"`
	Communities        int    `json:"communities"`
	Hubs               int    `json:"hubs"`
	Incidents          int    `json:"incidents"`
	Memories           int    `json:"memories"`
	ApprovalsPending   int    `json:"approvals_pending"`
	ArchitectureErrors int    `json:"architecture_errors"`
	ArchitectureWarn   int    `json:"architecture_warnings"`
	GeneratedAt        string `json:"generated_at"`
}

// buildOverview computes the aggregate overview numbers.
func (a *App) buildOverview() overviewData {
	incidents, _ := a.inter.List()
	memories, _ := a.memories.List("")
	errCount, warnCount := 0, 0
	if rep, err := a.buildArchitecture(); err == nil {
		errCount = rep.ErrorCount
		warnCount = rep.WarningCount
	}
	g, ix := a.freshGraph()
	return overviewData{
		Root:               a.root,
		Symbols:            len(ix.Symbols),
		Files:              len(ix.SymbolsByFile),
		Edges:              len(g.Edges),
		Communities:        len(intel.Communities(ix)),
		Hubs:               len(intel.Hubs(ix, 5)),
		Incidents:          len(incidents),
		Memories:           len(memories),
		ApprovalsPending:   len(a.approvals.Pending()),
		ArchitectureErrors: errCount,
		ArchitectureWarn:   warnCount,
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// graphData is the shape of /api/graph.
type graphData struct {
	Hubs        []intel.Hub       `json:"hubs"`
	Communities []intel.Community `json:"communities"`
}

// buildGraph returns the top hubs and all communities.
func (a *App) buildGraph(limit int) graphData {
	_, ix := a.freshGraph()
	return graphData{
		Hubs:        intel.Hubs(ix, limit),
		Communities: intel.Communities(ix),
	}
}

// memoryData is the shape of /api/memory.
type memoryData struct {
	Items []memoryMemory `json:"items"`
}

// memoryMemory is a stable JSON projection of a domain.Memory.
type memoryMemory struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Scope     string    `json:"scope"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// buildMemory returns the typed memories for the project.
func (a *App) buildMemory() memoryData {
	ms, err := a.memories.List("")
	if err != nil {
		return memoryData{Items: []memoryMemory{}}
	}
	out := make([]memoryMemory, 0, len(ms))
	for _, m := range ms {
		out = append(out, memoryMemory{
			ID:        m.ID,
			Type:      string(m.Type),
			Content:   m.Content,
			Source:    m.Source,
			Scope:     m.Scope,
			Tags:      m.Tags,
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return memoryData{Items: out}
}

// incidentSummary is a flattened, read-only projection of a domain.Incident.
type incidentSummary struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Severity        string    `json:"severity"`
	Status          string    `json:"status"`
	AffectedService string    `json:"affected_service"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// buildIncidents returns a flattened summary list of persisted incidents.
func (a *App) buildIncidents() ([]incidentSummary, error) {
	list, err := a.inter.List()
	if err != nil {
		return nil, err
	}
	out := make([]incidentSummary, 0, len(list))
	for _, inc := range list {
		out = append(out, incidentSummary{
			ID:              inc.ID,
			Title:           inc.Title,
			Severity:        string(inc.Severity),
			Status:          string(inc.Status),
			AffectedService: inc.AffectedService,
			UpdatedAt:       inc.UpdatedAt,
		})
	}
	return out, nil
}

// architectureViolation is the JSON projection of an architecture.Violation.
type architectureViolation struct {
	CallerFile string `json:"caller_file"`
	CalleeFile string `json:"callee_file"`
	Symbol     string `json:"symbol"`
	RuleFrom   string `json:"rule_from"`
	RuleTo     string `json:"rule_to"`
	RuleID     string `json:"rule_id"`
	Severity   string `json:"severity"`
}

// architectureData is the shape of /api/architecture.
type architectureData struct {
	OK           bool                    `json:"ok"`
	ErrorCount   int                     `json:"error_count"`
	WarningCount int                     `json:"warning_count"`
	Violations   []architectureViolation `json:"violations"`
}

// buildArchitecture runs the architecture validation and returns the report.
// It validates against the current index (refreshed on demand so edits since
// startup are reflected) instead of re-indexing the whole repo per request.
// The result is still cached for archTTL so a polled dashboard stays
// consistent within a short window.
func (a *App) buildArchitecture() (*architectureData, error) {
	a.archMu.Lock()
	defer a.archMu.Unlock()
	if a.archRep != nil && time.Since(a.archAt) < a.archTTL {
		return a.archRep, nil
	}
	_, ix := a.freshGraph()
	rep, err := architecture.ValidateProjectWithIndex(a.root, ix)
	if err != nil {
		return nil, err
	}
	violations := make([]architectureViolation, 0, len(rep.Violations))
	for _, v := range rep.Violations {
		violations = append(violations, architectureViolation{
			CallerFile: v.CallerFile,
			CalleeFile: v.CalleeFile,
			Symbol:     v.Symbol,
			RuleFrom:   v.RuleFrom,
			RuleTo:     v.RuleTo,
			RuleID:     v.RuleID,
			Severity:   v.Severity,
		})
	}
	out := &architectureData{
		OK:           rep.OK,
		ErrorCount:   rep.ErrorCount,
		WarningCount: rep.WarningCount,
		Violations:   violations,
	}
	a.archRep = out
	a.archAt = time.Now()
	return out, nil
}

// governancePolicy is a projection of a domain.Policy.
type governancePolicy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
}

// governanceData is the shape of /api/governance.
type governanceData struct {
	Policies         []governancePolicy     `json:"policies"`
	ApprovalsPending []domainApproval       `json:"approvals_pending"`
	Audit            []governanceAuditEntry `json:"audit"`
}

// domainApproval is the JSON projection of a domain.Approval.
type domainApproval struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"task_id"`
	Requester   string     `json:"requester"`
	Approver    string     `json:"approver"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	RequestedAt time.Time  `json:"requested_at"`
	DecidedAt   *time.Time `json:"decided_at"`
}

// governanceAuditEntry is the JSON projection of a governance.AuditEntry.
type governanceAuditEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agent_id"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Approved  bool      `json:"approved"`
	Result    string    `json:"result"`
}

// buildApprovals extracts the pending-approval projection list.
func (a *App) buildApprovals() []domainApproval {
	approvals := make([]domainApproval, 0, len(a.approvals.Pending()))
	for _, ap := range a.approvals.Pending() {
		approvals = append(approvals, domainApproval{
			ID:          ap.ID,
			TaskID:      ap.TaskID,
			Requester:   ap.Requester,
			Approver:    ap.Approver,
			Status:      ap.Status,
			Reason:      ap.Reason,
			RequestedAt: ap.RequestedAt,
			DecidedAt:   ap.DecidedAt,
		})
	}
	return approvals
}

// buildGovernance assembles the governance panel data.
func (a *App) buildGovernance() governanceData {
	policies := governance.DefaultPolicies()
	out := make([]governancePolicy, 0, len(policies))
	for _, p := range policies {
		out = append(out, governancePolicy{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Scope:       p.Scope,
		})
	}
	audit := make([]governanceAuditEntry, 0)
	for _, e := range a.firewall.AuditLog().All() {
		audit = append(audit, governanceAuditEntry{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			AgentID:   e.AgentID,
			Action:    e.Action,
			Resource:  e.Resource,
			Approved:  e.Approved,
			Result:    e.Result,
		})
	}
	return governanceData{Policies: out, ApprovalsPending: a.buildApprovals(), Audit: audit}
}

// dashboardData is the template model for the HTML dashboard.
type dashboardData struct {
	Root         string
	Overview     overviewData
	Graph        graphData
	Memory       memoryData
	Incidents    []incidentSummary
	Architecture *architectureData
	Governance   governanceData
}

// buildDashboard assembles the full server-side dashboard data.
func (a *App) buildDashboard() (*dashboardData, error) {
	incidents, err := a.buildIncidents()
	if err != nil {
		return nil, err
	}
	arch, err := a.buildArchitecture()
	if err != nil {
		return nil, err
	}
	return &dashboardData{
		Root:         a.root,
		Overview:     a.buildOverview(),
		Graph:        a.buildGraph(10),
		Memory:       a.buildMemory(),
		Incidents:    incidents,
		Architecture: arch,
		Governance:   a.buildGovernance(),
	}, nil
}

// taskDetailData holds the 13 lifecycle fields for the task detail page.
type taskDetailData struct {
	TaskID    string
	State     string
	Intent    string
	Type      string
	CreatedBy string
	CreatedAt string
	UpdatedAt string
	AgentID   string
	Steps     []stepRow

	HasContext     bool
	ContextSymbols int
	ContextFiles   int
	ContextEdges   int
	ContextTokens  int

	Memories    []memoryRow
	MemoryCount int

	HasImpact     bool
	ImpactRisk    string
	ImpactAffected int
	ImpactServices int

	Risks []riskRow

	ApprovalStatus   string
	ApprovalApprover string
	ApprovalReason   string

	Artifacts []artifactRow

	HasVerification     bool
	VerificationVerdict string
	VerificationSummary string

	PRURL    string
	PRNumber int

	DeploymentVersion string
	DeploymentStatus  string
	ObservationResult string
}

type stepRow struct {
	Index   int
	Action  string
	AgentID string
	Status  string
	Result  string
}

type memoryRow struct {
	Type    string
	Scope   string
	Content string
}

type riskRow struct {
	Severity    string
	Description string
	Mitigation  string
}

type artifactRow struct {
	Kind    string
	Summary string
	Source  string
	AgentID string
}

// buildTaskDetailData projects an agent.Task into the template model for the
// task detail page. It is read-only and degrades gracefully when lifecycle
// outputs are nil or empty (the template renders "not assembled"/"not
// assessed"/etc. placeholders).
func (a *App) buildTaskDetailData(task *agent.Task) taskDetailData {
	d := taskDetailData{
		TaskID:    task.ID,
		State:     string(task.State),
		Intent:    task.Intent,
		Type:      task.Type,
		CreatedBy: task.CreatedBy,
		CreatedAt: task.CreatedAt.Format(time.RFC3339),
		UpdatedAt: task.UpdatedAt.Format(time.RFC3339),
		AgentID:   task.AgentID,
	}

	// Steps (includes the step AgentIDs for Field 3).
	for _, s := range task.Steps {
		d.Steps = append(d.Steps, stepRow{
			Index:   s.Index,
			Action:  s.Action,
			AgentID: s.AgentID,
			Status:  s.Status,
			Result:  s.Result,
		})
	}

	// Context packet (Field 4).
	if task.ContextPacket != nil {
		d.HasContext = true
		d.ContextSymbols = len(task.ContextPacket.Symbols)
		d.ContextFiles = len(task.ContextPacket.Files)
		d.ContextEdges = len(task.ContextPacket.Dependencies)
		d.ContextTokens = task.ContextPacket.TokenCount
	}

	// Memory (Field 5) — recall relevant memories for the task intent.
	if a.memories != nil && task.Intent != "" {
		mems, _ := a.memories.Recall(memory.Query{Text: task.Intent, Limit: 10})
		d.MemoryCount = len(mems)
		for _, m := range mems {
			d.Memories = append(d.Memories, memoryRow{
				Type:    string(m.Type),
				Scope:   m.Scope,
				Content: m.Content,
			})
		}
	}

	// Impact (Field 6).
	if task.ImpactReport != nil {
		d.HasImpact = true
		d.ImpactRisk = task.ImpactReport.Risk
		d.ImpactAffected = len(task.ImpactReport.Affected)
		d.ImpactServices = len(task.ImpactReport.Services)
	}

	// Risks (Field 7).
	for _, r := range task.Risks {
		d.Risks = append(d.Risks, riskRow{
			Severity:    string(r.Level),
			Description: strings.Join(r.Factors, "; "),
			Mitigation:  r.Mitigation,
		})
	}

	// Approval (Field 8) — check pending approvals matching this task.
	if a.approvals != nil {
		for _, ap := range a.approvals.Pending() {
			if ap.TaskID == task.ID {
				d.ApprovalStatus = ap.Status
				d.ApprovalApprover = ap.Requester
				d.ApprovalReason = ap.Reason
				break
			}
		}
	}
	if d.ApprovalStatus == "" {
		// Fall back to any step with an "approve" action that succeeded.
		for _, s := range task.Steps {
			if s.Action == "approve" && s.Status == "success" {
				d.ApprovalStatus = "approved"
				d.ApprovalApprover = s.AgentID
				break
			}
		}
	}

	// Artifacts (Field 9).
	if a.taskSvc != nil {
		arts, _ := a.taskSvc.Artifacts().GetByTask(task.ID)
		for _, art := range arts {
			d.Artifacts = append(d.Artifacts, artifactRow{
				Kind:    string(art.Kind),
				Summary: art.URI,
				Source:  art.Provenance,
				AgentID: art.CreatedBy,
			})
		}
	}

	// Verification (Field 10).
	if task.Verification != nil {
		d.HasVerification = true
		d.VerificationVerdict = string(task.Verification.Verdict)
		d.VerificationSummary = task.Verification.Summary
	}

	// PR (Field 11).
	d.PRURL = task.PRURL
	d.PRNumber = task.PRNumber

	// Deployment (Field 12) — scan steps for a deploy action.
	for _, s := range task.Steps {
		if s.Action == "deploy" {
			d.DeploymentVersion = s.Result
			d.DeploymentStatus = s.Status
			break
		}
	}

	// Production outcome (Field 13) — scan steps for an observe action.
	for _, s := range task.Steps {
		if s.Action == "observe" {
			d.ObservationResult = s.Result
			break
		}
	}

	return d
}
