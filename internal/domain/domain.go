// Package domain defines the canonical Kern 2.0 domain model. Domain types are
// pure — no MCP, CLI, REST, or storage specifics — so every interface reuses
// the same types.
package domain

import "time"

// Project is a software project being analyzed by kern.
type Project struct {
	Root        string    // absolute path to project root
	Name        string    // project name (derived from dir or VCS)
	Languages   []string  // detected languages
	Frameworks  []string  // detected framework IDs
	VCS         string    // "git", "none", etc.
	IndexedAt   time.Time // last index build time
	SymbolCount int       // total indexed symbols
}

// Repository is a VCS repository. A Project may have one.
type Repository struct {
	Root   string // absolute path
	VCS    string // "git", etc.
	Remote string // origin URL if available
	Branch string // current branch
	HEAD   string // current commit hash
}

// Module is a logical grouping of files (Go package, JS module, Python
// package, etc.).
type Module struct {
	Path     string // import path or module path
	Language string
	Files    []string // file paths
	Symbols  []Symbol
}

// File is a source file in the project.
type File struct {
	Path     string // relative to project root
	Language string
	Hash     string // content hash (for staleness detection)
	Symbols  []Symbol
	Lines    int
	Owner    string // CODEOWNERS owner handle (e.g. "@backend-team"), empty when unknown
}

// Symbol is a code symbol: function, class, method, type, etc.
type Symbol struct {
	Name      string // simple name
	Qualified string // qualified name (package.Name or Type.Method)
	Kind      string // "func", "method", "class", "type", "const", "var", etc.
	File      string // relative file path
	Line      int    // definition line
	Language  string
	Signature string // function signature if applicable
	Receiver  string // method receiver if applicable
	Exported  bool
}

// Graph is the unified knowledge graph — the canonical type for all code
// relationships.
type Graph struct {
	Project Project
	Nodes   []Node
	Edges   []Edge
	// Optional pointers so the zero value remains valid.
	Provenance *Provenance
	Version    *VersionMetadata
}

// Node is a node in the knowledge graph.
type Node struct {
	ID       string    // stable identifier (qualified name or path)
	Kind     string    // "symbol", "file", "module", "api", "db", "topic", "service", "deployment"
	Label    string    // display name
	Symbol   *Symbol   // non-nil for code nodes
	File     *File     // non-nil for file nodes
	Database *Database // non-nil for "database" nodes
	Table    *Table    // non-nil for "table" nodes
	Topic    *Topic    // non-nil for "topic" nodes
	API      *API      // non-nil for "api" nodes
	Service  *Service  // non-nil for "service" nodes
	Team     *Team     // non-nil for "team" nodes
	// TODO(phase 11+): Deployment node kinds.
}

// Edge is an edge in the knowledge graph.
type Edge struct {
	From string // node ID
	To   string // node ID
	Kind string // "calls", "depends_on", "inherits", "imports", "contains", "defines"
	File string // where the edge originates
	Line int
}

// ClaimType discriminates the kind of claim a Claim represents.
type ClaimType string

const (
	ClaimFact           ClaimType = "FACT"           // verified deterministic fact
	ClaimInference      ClaimType = "INFERENCE"      // derived from facts
	ClaimHypothesis     ClaimType = "HYPOTHESIS"     // unverified proposition
	ClaimRecommendation ClaimType = "RECOMMENDATION" // suggested action
)

// Claim is a typed claim about the software system. Every high-value analysis
// output is a Claim.
type Claim struct {
	Type       ClaimType  // FACT, INFERENCE, HYPOTHESIS, RECOMMENDATION
	Statement  string     // the claim text
	Evidence   []Evidence // supporting evidence
	Source     string     // what produced this claim (tool name, agent ID)
	Provenance string     // how the claim was derived
	Timestamp  time.Time
	Scope      string  // what the claim applies to (symbol, file, service)
	Confidence float64 // 0.0-1.0, where applicable
}

// HasEvidence reports whether the claim carries at least one piece of
// supporting evidence.
func (c Claim) HasEvidence() bool {
	return len(c.Evidence) > 0
}

// EvidenceType discriminates the kind of evidence backing a Claim.
type EvidenceType string

const (
	EvidenceGraph   EvidenceType = "graph"   // call graph, impact analysis
	EvidenceTest    EvidenceType = "test"    // test result
	EvidenceBuild   EvidenceType = "build"   // build result
	EvidenceGit     EvidenceType = "git"     // commit, diff, history
	EvidenceRuntime EvidenceType = "runtime" // metrics, traces, logs (Phase 11)
	EvidenceMemory  EvidenceType = "memory"  // historical decision/incident
	EvidencePolicy  EvidenceType = "policy"  // policy evaluation result
)

// Evidence supports a Claim.
type Evidence struct {
	Type         EvidenceType // "graph", "test", "build", "git", "runtime", "memory", "policy"
	Source       string       // tool/file/commit that produced this evidence
	Content      string       // the evidence content (code snippet, test result, etc.)
	Relationship string       // specific relationship kind: "calls", "depends_on", "inherits", "changed_by", etc. (empty when N/A)
	Digest       string       // content hash for integrity
	Timestamp    time.Time
}

// MemoryType discriminates the kind of engineering memory a Memory holds.
type MemoryType string

const (
	MemoryLesson     MemoryType = "lesson"     // existing — current memory/
	MemoryDecision   MemoryType = "decision"   // ADR-like
	MemoryIncident   MemoryType = "incident"   // post-mortem
	MemoryConstraint MemoryType = "constraint" // "Redis keys must contain tenant ID"
	MemorySemantic   MemoryType = "semantic"   // general knowledge
	MemoryAgent      MemoryType = "agent"      // agent-specific history
	MemoryEpisodic   MemoryType = "episodic"   // a specific event/episode with temporal context (raw, not derived)
)

// Memory is engineering memory — it generalizes the current lesson-only
// memory.
type Memory struct {
	ID        string
	Type      MemoryType
	Content   string
	Source    string // agent ID or "human"
	Scope     string // project, service, symbol, etc.
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time

	Subject         string   // subject the memory is about (symbol, file, service, task)
	Confidence      float64  // 0.0-1.0 how confident the memory is
	Provenance      string   // how the memory was derived (e.g. "loop:learn", "human", "incident")
	RelatedEntities []string // related entity IDs (symbols, tasks, PRs, services)

	// Reason holds the rationale behind a MemoryDecision (spec §8 F-25),
	// separate from Content which holds the decision text itself. Empty for
	// other memory types.
	Reason string
	// Classification is the security classification of the memory (spec §41
	// F-55): "public", "internal", "confidential", or "restricted". Empty
	// means unclassified (accessible to all).
	Classification string
}

// Classification levels for Memory (spec §41 F-55). Empty Classification is
// treated as unclassified ("public", level 0).
const (
	ClassificationPublic       = "public"
	ClassificationInternal     = "internal"
	ClassificationConfidential = "confidential"
	ClassificationRestricted   = "restricted"
)

// Decision is an engineering decision (ADR-like).
type Decision struct {
	ID           string
	Title        string
	Context      string
	Decision     string
	Consequences string
	Status       string // "proposed", "accepted", "deprecated"
	Author       string
	CreatedAt    time.Time
}

// Policy is a governance policy rule.
type Policy struct {
	ID          string
	Name        string
	Description string
	Rule        string // YAML rule body (risk level, approval required, etc.)
	Scope       string // what the policy applies to
	Enabled     bool
}

// RiskLevel discriminates the severity of a Risk.
type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

// Risk is a risk assessment for a proposed change.
type Risk struct {
	Level      RiskLevel // LOW, MEDIUM, HIGH, CRITICAL
	Factors    []string  // what contributes to the risk
	Score      float64   // 0.0-1.0
	Mitigation string

	// Blocked is true when the governing firewall denied the change outright
	// (unknown agent, missing permission, always-blocked action, or an
	// approval gate that is still pending). When set, the change must not
	// proceed.
	Blocked bool
	// ApprovalRequired is true when the change risk is HIGH/CRITICAL and the
	// governance model requires human approval before the change may proceed.
	ApprovalRequired bool
}

// Approval is an approval for a high-risk action.
//
// Phase 9: the Integration Transformation Plan's Approval Model (§16) requires
// approval to bind to task, agent, requested action, risk, policy result,
// evidence, AND artifact — not a bare approved=true. The RiskLevel, PolicyIDs,
// EvidenceRefs, and ArtifactID fields make the binding explicit so an auditor
// can reconstruct WHY an approval was granted and WHAT it authorized.
type Approval struct {
	ID          string
	TaskID      string
	Requester   string // agent ID
	Approver    string // human ID
	Status      string // "pending", "approved", "rejected"
	Reason      string
	RequestedAt time.Time
	DecidedAt   *time.Time

	// Phase 9 binding fields — populate when the approval is requested so the
	// decision carries full context for audit, resume, and debugging.
	RiskLevel    RiskLevel // the risk level that triggered the approval gate
	PolicyIDs    []string  // policy IDs that evaluated to this risk
	EvidenceRefs []string  // evidence/claim IDs supporting the risk assessment
	ArtifactID   string    // the artifact (e.g. ImpactReport) backing the approval
}

// Agent is an AI agent identity.
type Agent struct {
	ID        string
	Name      string
	Type      string // "planner", "coder", "reviewer", "sre", etc.
	CreatedAt time.Time
	// TODO(phase 6): Permissions []Permission added with the Phase 6
	// Permission/AuditEntry entities.
}

// TaskState discriminates the lifecycle state of a Task.
type TaskState string

const (
	TaskCreated         TaskState = "CREATED"
	TaskAnalyzing       TaskState = "ANALYZING"
	TaskPlanning        TaskState = "PLANNING"
	TaskWaitingApproval TaskState = "WAITING_FOR_APPROVAL"
	TaskApproved        TaskState = "APPROVED"
	TaskExecuting       TaskState = "EXECUTING"
	TaskVerifying       TaskState = "VERIFYING"
	TaskReadyForPR      TaskState = "READY_FOR_PR"
	TaskPRCreated       TaskState = "PR_CREATED"
	TaskDeploying       TaskState = "DEPLOYING"
	TaskObserving       TaskState = "OBSERVING"
	TaskCompleted       TaskState = "COMPLETED"
	TaskFailed          TaskState = "FAILED"
	TaskBlocked         TaskState = "BLOCKED"
	TaskRejected        TaskState = "REJECTED"
	TaskCancelled       TaskState = "CANCELLED"
	TaskRolledBack      TaskState = "ROLLED_BACK"
)

// Task is a unit of work in the agent workflow.
type Task struct {
	ID        string
	Type      string // "analyze", "plan", "code", "verify", "deploy"
	State     TaskState
	Agent     *Agent
	Input     string // task description
	Output    string // result
	CreatedAt time.Time
	UpdatedAt time.Time
	// PriorState records the state before BLOCKED, so Resume can return to it.
	PriorState TaskState
	// TODO(phase 5): Context *ContextPacket added with the Phase 5 context
	// engine entity.
}

// IsTerminal reports whether the task has reached a truly final state from
// which no recovery (retry/resume) is possible. FAILED and BLOCKED are
// recoverable: Retry reopens FAILED → ANALYZING; Resume reopens BLOCKED →
// PriorState. Only COMPLETED, CANCELLED, REJECTED, and ROLLED_BACK are final.
func (t Task) IsTerminal() bool {
	switch t.State {
	case TaskCompleted, TaskCancelled, TaskRejected, TaskRolledBack:
		return true
	}
	return false
}

// Plan is the structured implementation plan produced by the control-plane
// Plan workflow (Integration Transformation Plan Phase 6). It is assembled
// deterministically from the analyze context packet, impact report, risk
// assessment, and architecture rules — the LLM may explain it, but the
// fields are populated from deterministic sources.
//
// The 12 fields mirror the spec's Plan artifact contract (§11): Objective,
// Scope, AffectedComponents, ImplementationSteps, Dependencies, Risk,
// Rollback, Tests, Security, Architecture, Deployment, Evidence.
type Plan struct {
	Objective          string   // what the change achieves
	Scope              string   // boundary of the change
	AffectedComponents []string // symbols/files/packages touched
	ImplementationSteps []string // ordered steps to implement
	Dependencies       []string // upstream changes required
	Risk               string   // low | medium | high (from risk assessment)
	Rollback           string   // how to undo
	Tests              []string // test cases to add/run
	Security           string   // security considerations
	Architecture       string   // architecture-rule compliance notes
	Deployment         string   // deployment considerations
	Evidence           []string // claim/evidence IDs backing the plan
}

// ImpactReport answers the 11 deterministic impact questions from the
// Integration Transformation Plan Phase 7 (§12). Each field is populated
// directly from the knowledge graph — no LLM is the authoritative source.
// The LLM may explain the results, but the data is deterministic.
type ImpactReport struct {
	Target           string   // the symbol the change targets
	WhoCalls         []string // what calls this
	WhatItCalls      []string // what does it call
	ServicesDepend   []string // what services depend on it
	APIsAffected     []string // which APIs are affected
	DataStoresAffected []string // which data stores are affected
	EventsAffected   []string // which events are affected
	TestsCover       []string // which tests cover it
	DeploymentsRelated []string // which deployments are related
	IncidentsRelated []string // which incidents are related
	ArchitectureRules  []string // which architecture rules apply
	Risk             string   // low | medium | high (from criticality)
}
