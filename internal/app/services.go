// Package app service contracts.
// This file defines the 15 discrete application-service interfaces (
// of the ). They are the contract every
// interface layer (MCP, CLI, REST) uses, and they are all satisfied by the
// existing *TaskService and *Platform types WITHOUT engine changes — the
// interfaces capture the exact method signatures those types already expose.
// The P2 exit gate — "no core business workflow exists only in one interface" —
// is enforced by having each interface route through these shared services.
// Adding a method here requires implementing it on *TaskService or *Platform,
// so the orchestration lives in exactly one place.
package app

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Task is the task-lifecycle application service. It is the authoritative
// record every interface (CLI, MCP, REST) creates, progresses, and persists a
// Task through, so analysis/plan/impact/verify always land in the same store.
// Implemented by *TaskService.
type Task interface {
	Create(intent string) (*agent.Task, error)
	Get(id string) (*agent.Task, bool)
	List() []*agent.Task
	Run(intent string) (*domain.RunResult, error)
	Cancel(taskID, reason string) error
	Timeout(taskID string) error
	Retry(taskID string) (*agent.Task, error)
	Resume(taskID string) (*agent.Task, error)
	Pause(taskID, reason string) error
	Rollback(taskID, reason string) error
	HumanTakeover(taskID, agentID string) error
}

// Context is the context-analysis contract service: it runs the context engine
// against a proposed change and returns the assembled ContextPacket plus its
// rendered text. It is the stateless fast path that TaskService.Analyze wraps
// with Task lifecycle management. Implemented by *Platform.
type Context interface {
	// Analyze assembles a ContextPacket for a proposed change.
	Analyze(change string) (domain.ContextPacket, string, error)
}

// Analysis is the task-tracked analysis contract service. It is the single
// authoritative Analyze workflow shared by handleAnalyze (MCP) and
// `kern analyze` (CLI): it creates a Task, runs the context engine, attaches
// the ContextPacket, and completes the Task so the analysis is queryable via
// Task.Get. Implemented by *TaskService.
type Analysis interface {
	// Analyze creates an authoritative Task and returns it plus the rendered
	// analysis text.
	Analyze(intent string) (*agent.Task, string, error)
}

// Impact is the graph-impact contract service. It runs the 11 deterministic
// graph queries from the spec and attaches the ImpactReport to a Task.
// Implemented by *TaskService.
type Impact interface {
	// Impact returns the Task and its deterministic ImpactReport + rendered text.
	// Options (e.g. ImpactStrict) customize the computation.
	Impact(change string, opts ...ImpactOption) (*agent.Task, domain.ImpactReport, string, error)
}

// WhatIf is the what-if simulation contract service. It simulates a
// hypothetical change against the knowledge graph and attaches the result to a
// Task. Implemented by *TaskService.
type WhatIf interface {
	// WhatIf simulates a change and returns the Task plus the rendered impact.
	WhatIf(kind whatif.ChangeKind, change, newTarget string) (*agent.Task, string, error)
}

// Memory is the engineering-memory contract service. It makes recall a first
// class application service so every interface reads the same memory the
// analysis/incident engines consume. Implemented by *TaskService.
type Memory interface {
	// MemoryRecall returns the most relevant past lessons for a query.
	MemoryRecall(query string) []string
	// MemoryStore returns the underlying shared memory store.
	MemoryStore() *memory.MemoryStore
}

// Risk is the risk-assessment contract service. It returns the focused risk
// view for a proposed change, shared by `kern risk` (CLI) and `POST /v1/risk`
// (REST). Implemented by *TaskService.
type Risk interface {
	// Risk returns the ContextPacket plus a focused risk render for a change.
	Risk(change string) (domain.ContextPacket, string, error)
}

// Policy is the governance-policy contract service. It exposes the single
// shared firewall so every interface gates risk/permission/approval through the
// same governance layer. Implemented by *TaskService.
type Policy interface {
	// Firewall returns the shared governance firewall.
	Firewall() *governance.Firewall
	// PolicyPrecheck runs the unified identity/scope/permission/environment/risk
	// precheck and returns the single PrecheckResult.
	PolicyPrecheck(ctx context.Context, req domain.PrecheckRequest) domain.PrecheckResult
}

// Agent is the specialist-agent contract service. It lists the available
// specialist roles and exposes the task registry backing the service.
// Implemented by *TaskService.
type Agent interface {
	// Registry returns the task registry backing this service.
	Registry() *agent.Registry
	// Agents returns the standard specialist team role list.
	Agents() []agents.RoleInfo
}

// Execution is the sandboxed-execution contract service. It applies a patch in
// a worktree gated by governance and records the diff as an artifact.
// Implemented by *TaskService.
type Execution interface {
	// Execute applies a patch in a sandboxed worktree and returns the diff.
	Execute(patch string) (*agent.Task, string, error)
	// ExecuteAndVerify applies a patch and verifies the worktree in one pass.
	ExecuteAndVerify(patch string, verifyTypes []string) (*agent.Task, string, verification.VerificationResult, error)
}

// Loop is the closed-loop contract service. It runs the continuous closed loop
// under an authoritative Task so every interface gets task tracking and an audit
// trail instead of orchestrating the loop engine inline. Implemented by
// *TaskService.
type Loop interface {
	// RunLoop creates a Task and runs the closed loop at the given autonomy
	// level, returning the Task and the loop Result for rendering.
	RunLoop(intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error)
}

// Verification is the verification contract service. It runs the verification
// engine and gates Task completion on the verdict. Implemented by *TaskService.
type Verification interface {
	// Verify runs verification and returns the Task plus the result.
	Verify(types []string) (*agent.Task, verification.VerificationResult, error)
	// VerifyTask verifies a Task's worktree and transitions to READY_FOR_PR.
	VerifyTask(taskID string, worktreeDir string, types []string) (*agent.Task, verification.VerificationResult, error)
}

// Incident is the incident-management contract service. It correlates alerts
// and runs the full incident workflow on the authoritative Task. Implemented by
// *TaskService.
type Incident interface {
	// Correlate runs the runtime correlation engine against an alert.
	Correlate(alert domain.Alert) (*agent.Task, runtime.CorrelationChain, string, error)
	// InvestigateIncident runs the full incident workflow (ingest→correlate→root cause).
	InvestigateIncident(alert domain.Alert) (*agent.Task, *domain.Incident, string, error)
}

// Modernization is the legacy-modernization contract service. It runs the
// modernization analysis and records it as an auditable Task. Implemented by
// *TaskService.
type Modernization interface {
	// Modernize runs the modernization analysis.
	Modernize() (*agent.Task, modernization.ExtractionPlan, string, error)
}

// Deployment is the deployment contract service. It deploys a verified Task and
// observes it through the lifecycle. Implemented by *TaskService.
type Deployment interface {
	// Deploy deploys a verified Task.
	Deploy(taskID, version string) (*agent.Task, error)
	// Observe transitions a deployed Task to COMPLETED.
	Observe(taskID string) (*agent.Task, error)
}

// Audit is the audit contract service. It exposes the single tamper-evident
// governance audit log so every interface reads the same authoritative trail.
// Implemented by *TaskService.
type Audit interface {
	// AuditLog returns the shared governance audit log.
	AuditLog() *governance.AuditLog
}

// Compile-time assertions: the existing concrete services satisfy the 15
// contracts without engine changes. A compile failure here means an interface
// drifted from the real *TaskService / *Platform signatures.
var (
	_ Task          = (*TaskService)(nil)
	_ Context       = (*Platform)(nil)
	_ Analysis      = (*TaskService)(nil)
	_ Impact        = (*TaskService)(nil)
	_ WhatIf        = (*TaskService)(nil)
	_ Memory        = (*TaskService)(nil)
	_ Risk          = (*TaskService)(nil)
	_ Policy        = (*TaskService)(nil)
	_ Agent         = (*TaskService)(nil)
	_ Execution     = (*TaskService)(nil)
	_ Loop          = (*TaskService)(nil)
	_ Verification  = (*TaskService)(nil)
	_ Incident      = (*TaskService)(nil)
	_ Modernization = (*TaskService)(nil)
	_ Deployment    = (*TaskService)(nil)
	_ Audit         = (*TaskService)(nil)
)
