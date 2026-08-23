// This file defines the runtime domain entities for production operations:
// Alert, Deployment, Incident, Hypothesis, and RootCause.
package domain

import "time"

// Severity is the canonical incident severity level.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Alert is a canonical production alert that may lead to an incident.
type Alert struct {
	ID         string
	Severity   Severity
	Message    string
	Service    string // affected service name (may be empty until correlation)
	Source     string // e.g. "kubernetes", "opentelemetry", "sentry", "prometheus"
	OccurredAt time.Time
}

// Deployment describes one deployment of a service.
type Deployment struct {
	Service    string
	CommitSHA  string
	Version    string
	DeployedAt time.Time
}

// IncidentStatus is the lifecycle state of an incident.
type IncidentStatus string

const (
	IncidentOpen           IncidentStatus = "OPEN"
	IncidentInvestigating  IncidentStatus = "INVESTIGATING"
	IncidentRootCauseFound IncidentStatus = "ROOT_CAUSE_FOUND"
	IncidentFixProposed    IncidentStatus = "FIX_PROPOSED"
	IncidentFixApproved    IncidentStatus = "FIX_APPROVED"
	IncidentFixVerified    IncidentStatus = "FIX_VERIFIED"
	IncidentPRCreated      IncidentStatus = "PR_CREATED"
	IncidentClosed         IncidentStatus = "CLOSED"
	// IncidentFixBlocked indicates the fix pipeline's risk step (Phase 11)
	// denied the candidate fix (governance/risk) before verification.
	IncidentFixBlocked IncidentStatus = "FIX_BLOCKED"
)

// Hypothesis is a candidate explanation for an incident, evidence-backed and
// carrying a deterministic confidence (FACT / INFERENCE / HYPOTHESIS /
// RECOMMENDATION).
type Hypothesis struct {
	Statement  string
	Source     string // code, deploy, infra, memory, logs, metrics, traces
	Confidence ClaimType
	Score      float64
	Evidence   []Evidence
}

// RootCause is the accepted explanation for an incident after correlation and
// root-cause analysis.
type RootCause struct {
	Summary    string
	Service    string
	Hypothesis Hypothesis
	Files      []string
	CommitSHA  string
	Evidence   []Evidence
}

// Incident is the canonical runtime incident entity for Workflow D.
type Incident struct {
	ID                 string
	Title              string
	Severity           Severity
	Status             IncidentStatus
	Alert              Alert
	AffectedService    string
	RelatedDeployments []Deployment
	Hypotheses         []Hypothesis
	RootCause          *RootCause
	FixDescription     string
	FixDiff            string
	Verification       string // human-readable verification summary
	PRBody             string // PR body once created
	PRURL              string // web URL of the created PR (empty when no real PR)
	PRNumber           int    // PR number (0 when no real PR was created)
	// FixRisk is the governance risk assessment of the candidate fix (Phase
	// 11). It is set by the fix pipeline's risk step: either the risk level
	// (e.g. "LOW") when the fix is allowed, or a blocking reason when the fix
	// was denied before verification.
	FixRisk string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Evidence           []Evidence
	Memories           []Memory
	// Claims produced during root-cause analysis (e.g. a Hypothesis per candidate).
	Claims []Claim
}
