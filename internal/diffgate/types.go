package diffgate

// This file carries the minimal change-governance data model the diff-gate
// checks need, extracted verbatim from the blueprint domain package during
// the pivot. The blueprint family moved to the standalone blueprint repo; the
// drift gates (catalog:doc, contracts:doc, architecture:ledger) keep their
// own copy so they stay self-contained. The wire shapes (JSON tags) are
// unchanged — findings produced here still round-trip with the blueprint
// format where the two surfaces interoperate.

// Source identifies what produced a change request.
type Source string

const (
	SourceAgent    Source = "agent"
	SourceIDE      Source = "ide"
	SourceHuman    Source = "human"
	SourceRefactor Source = "refactor"
	SourceDepBot   Source = "dep-bot"
	SourceCI       Source = "ci"
	SourceWatch    Source = "watch"
)

// Operation is the kind of file mutation being proposed.
type Operation string

const (
	OpWrite  Operation = "write"
	OpEdit   Operation = "edit"
	OpDelete Operation = "delete"
	OpRename Operation = "rename"
	OpCommit Operation = "commit"
)

// Severity rates how serious a finding is. Only SeverityBlock forces BLOCK aggregation.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
	SeverityBlock Severity = "block"
)

// Status is the final validation verdict.
type Status string

const (
	StatusPass  Status = "PASS"
	StatusWarn  Status = "WARN"
	StatusBlock Status = "BLOCK"
	StatusError Status = "ERROR"
	StatusSkip  Status = "SKIP"
)

// Category classifies a finding for routing, suppression, and policy matching.
type Category string

const (
	CategoryArchitecture Category = "architecture"
	CategorySecret       Category = "secret"
	CategoryDuplication  Category = "duplication"
	CategoryTests        Category = "tests"
	CategoryBuild        Category = "build"
	CategoryResilience   Category = "resilience"
	CategoryPolicy       Category = "policy"
	CategoryPerformance  Category = "performance"
	CategoryApproval     Category = "approval"
)

// FileChange describes one file mutation in a proposed change.
type FileChange struct {
	Path    string    `json:"path"`
	OldPath string    `json:"old_path,omitempty"`
	Op      Operation `json:"op"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
	Content string    `json:"content,omitempty"`
	Diff    string    `json:"diff,omitempty"`
}

// Evidence provides supporting context for a finding (never contains secrets).
type Evidence struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Location    string `json:"location,omitempty"`
}

// Finding is one issue discovered during validation.
type Finding struct {
	RuleID       string     `json:"rule_id"`
	Severity     Severity   `json:"severity"`
	Category     Category   `json:"category"`
	File         string     `json:"file"`
	Line         int        `json:"line,omitempty"`
	Column       int        `json:"column,omitempty"`
	Message      string     `json:"message"`
	Explanation  string     `json:"explanation,omitempty"`
	SuggestedFix string     `json:"suggested_fix,omitempty"`
	Evidence     []Evidence `json:"evidence,omitempty"`
	Redacted     bool       `json:"redacted,omitempty"`
	Suppressed   bool       `json:"suppressed,omitempty"`
	Owner        string     `json:"owner,omitempty"`

	// RuleVersion, Confidence and Scope are the Kern 2.0 provenance fields the
	// diff-gate checks stamp (kept for wire parity with the blueprint format).
	RuleVersion    string  `json:"rule_version,omitempty"`
	KernVersion    string  `json:"kern_version,omitempty"`
	IndexFreshness string  `json:"index_freshness,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Scope          string  `json:"scope,omitempty"`
}

// CheckResult is the outcome of one named check (e.g. "catalog:doc").
type CheckResult struct {
	Name     string    `json:"name"`
	Status   Status    `json:"status"`
	Findings []Finding `json:"findings,omitempty"`
	Duration int64     `json:"duration_ms"`
	Skipped  bool      `json:"skipped,omitempty"`
	Error    string    `json:"error,omitempty"`
	Source   Source    `json:"source,omitempty"`
}

// ChangeRequest is the normalized input to the validation pipeline.
type ChangeRequest struct {
	RepositoryRoot string            `json:"repository_root"`
	Source         Source            `json:"source"`
	AgentID        string            `json:"agent_id,omitempty"`
	Operation      Operation         `json:"operation"`
	Files          []FileChange      `json:"files"`
	BaseRevision   string            `json:"base_revision,omitempty"`
	TargetRevision string            `json:"target_revision,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}
