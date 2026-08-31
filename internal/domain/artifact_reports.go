// This file holds the concrete report struct types for artifact kinds that had
// no concrete domain type of their own ( Artifact contract). Like the
// rest of this package, these are pure types: storage-agnostic,
// provider-independent, and free of MCP, CLI, REST, or SDK specifics. Each type
// is a focused data carrier; the generic Artifact envelope (ID, Kind, Type,
// TaskID, Provenance, URI, Digest, ...) lives on Artifact itself.
// Types that already shipped (ContextPacket, ImpactReport, Plan, PullRequest,
// VerificationResult, Deployment, Incident, Risk, Claim, Evidence, Memory) are
// intentionally NOT re-defined here.
package domain

import "time"

// AnalysisReport captures the outcome of an analyze phase (ArtifactAnalysisReport).
type AnalysisReport struct {
	Summary      string   // high-level analysis summary
	Target       string   // the target scope analyzed (symbol, file, package)
	Findings     []string // findings/observations
	Symbols      []string // symbols examined
	Dependencies []string // dependencies examined
	Evidence     []string // evidence/claim IDs backing the analysis
	Risks        []string // risk IDs/summaries surfaced
	GeneratedAt  time.Time
}

// RiskReport is the risk assessment for a proposed change (ArtifactRiskReport).
type RiskReport struct {
	Target      string    `json:"target"`
	Level       string    `json:"level"`
	Risks       []Risk    `json:"risks"`
	Mitigations []string  `json:"mitigations"`
	Confidence  float64   `json:"confidence"`
	GeneratedAt time.Time `json:"generated_at"`
}

// PatchStats is a small set of diff/change statistics for a CodePatch.
type PatchStats struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// CodePatch is a proposed code change (ArtifactCodePatch).
type CodePatch struct {
	TaskID      string     `json:"task_id"`
	Files       []string   `json:"files"`
	Patch       string     `json:"patch"`
	Language    string     `json:"language"`
	Stats       PatchStats `json:"stats"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// Diff is a raw source difference (ArtifactDiff).
type Diff struct {
	Target      string    `json:"target"`
	OldHash     string    `json:"old_hash"`
	NewHash     string    `json:"new_hash"`
	Unified     string    `json:"unified"`
	Files       []string  `json:"files"`
	GeneratedAt time.Time `json:"generated_at"`
}

// TestCase is a single test case outcome within a TestReport.
type TestCase struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // "pass", "fail", "skip"
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail"`
}

// TestReport summarizes a test suite run (ArtifactTestReport).
type TestReport struct {
	Suite       string     `json:"suite"`
	Passed      int        `json:"passed"`
	Failed      int        `json:"failed"`
	Skipped     int        `json:"skipped"`
	DurationMS  int64      `json:"duration_ms"`
	Cases       []TestCase `json:"cases"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// SecurityFinding is a single vulnerability/policy finding.
type SecurityFinding struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Description string `json:"description"`
}

// SecurityReport is the result of a security scan (ArtifactSecurityReport).
type SecurityReport struct {
	Severity    string            `json:"severity"`
	Findings    []SecurityFinding `json:"findings"`
	Passed      bool              `json:"passed"`
	GeneratedAt time.Time         `json:"generated_at"`
}

// ArchitectureReport is the architecture-rule compliance result (ArtifactArchitectureReport).
type ArchitectureReport struct {
	Summary      string    `json:"summary"`
	RulesApplied int       `json:"rules_applied"`
	Violations   []string  `json:"violations"`
	Passed       bool      `json:"passed"`
	GeneratedAt  time.Time `json:"generated_at"`
}

// IncidentReport is a post-incident summary (ArtifactIncidentReport).
type IncidentReport struct {
	IncidentID  string    `json:"incident_id"`
	Severity    string    `json:"severity"`
	Service     string    `json:"service"`
	Summary     string    `json:"summary"`
	OpenedAt    time.Time `json:"opened_at"`
	Resolved    bool      `json:"resolved"`
	GeneratedAt time.Time `json:"generated_at"`
}

// RootCauseReport is the root-cause analysis result (ArtifactRootCauseReport).
type RootCauseReport struct {
	IncidentID  string    `json:"incident_id"`
	Hypothesis  string    `json:"hypothesis"`
	Evidence    []string  `json:"evidence"`
	Confidence  float64   `json:"confidence"`
	RootCause   string    `json:"root_cause"`
	GeneratedAt time.Time `json:"generated_at"`
}

// EvidenceReport consolidates evidence about a subject (ArtifactEvidenceReport).
type EvidenceReport struct {
	Subject     string    `json:"subject"`
	Claims      []Claim   `json:"claims"`
	Sources     []string  `json:"sources"`
	Confidence  float64   `json:"confidence"`
	GeneratedAt time.Time `json:"generated_at"`
}

// DeploymentReport is a deployment run summary (ArtifactDeploymentReport).
type DeploymentReport struct {
	DeploymentID string    `json:"deployment_id"`
	Service      string    `json:"service"`
	Version      string    `json:"version"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	GeneratedAt  time.Time `json:"generated_at"`
}

// RollbackReport describes a rollback of a deployment (ArtifactRollbackReport).
type RollbackReport struct {
	DeploymentID string    `json:"deployment_id"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	RolledBackAt time.Time `json:"rolled_back_at"`
	GeneratedAt  time.Time `json:"generated_at"`
}

// MemoryEntry is a thin supersession-aware memory artifact (ArtifactMemoryEntry).
// It intentionally reuses the memory concept without conflicting with the richer
// domain Memory type; the two coexist, MemoryEntry being the artifact-shaped view.
type MemoryEntry struct {
	ID         string    `json:"id"`
	Scope      string    `json:"scope"`
	Type       string    `json:"type"`
	Content    string    `json:"content"`
	Superseded bool      `json:"superseded"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditReport is the audit-trail summary artifact (ArtifactAudit). It
// finalizes a task's workflow by summarizing the artifact chain that was
// produced end-to-end, making the whole lifecycle auditable in one typed
// artifact .
type AuditReport struct {
	TaskID      string    `json:"task_id"`
	Summary     string    `json:"summary"`
	Artifacts   []string  `json:"artifacts"`
	ChainValid  bool      `json:"chain_valid"`
	GeneratedAt time.Time `json:"generated_at"`
}

// VerificationReport is the artifact-shaped view of a verification run
// (ArtifactVerificationReport, Artifact contract). It is the typed
// carrier recorded in the artifact chain for the verification stage, distinct
// from the engine's VerificationResult (which carries runtime check state).
// It carries the outcome verdict plus a stable snapshot of the sub-checks so a
// stored verification artifact is fully auditable and replayable.
type VerificationReport struct {
	ID          string              `json:"id"`      // verification run id
	TaskID      string              `json:"task_id"` // originating task (empty = ad-hoc)
	Verdict     string              `json:"verdict"` // "PASS", "FAIL", "WARN"
	Summary     string              `json:"summary"` // human-readable summary
	Checks      []VerificationCheck `json:"checks"`  // per sub-check outcomes
	Passed      bool                `json:"passed"`  // convenience: Verdict == "PASS"
	GeneratedAt time.Time           `json:"generated_at"`
}
