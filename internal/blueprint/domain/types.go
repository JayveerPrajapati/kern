// Package domain defines the canonical Blueprint change-governance data model.
//
// Every Blueprint adapter (CLI, MCP, Git hook, watcher, CI) ultimately produces
// or consumes these types. There is exactly one validation pipeline
// (spec Rule 1); this package is its lingua franca.
package domain

// Enforcement is how strongly a policy rule is enforced (spec Section 7).
type Enforcement string

const (
	EnforcementBlock Enforcement = "block"
	EnforcementWarn  Enforcement = "warn"
	EnforcementSkip  Enforcement = "skip"
)

// Source identifies what produced a change request (agent, human, CI, etc.).
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

// Status is the final validation verdict. Spec Section 5 mandates a small
// explicit state set; do not overload a single boolean.
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
	// CategoryApproval classifies the two-person approval gate (P1.3). The
	// check is named "approval:gate" so policy routing needs its own category
	// (the findings it emits carry CategoryPolicy). Default enforcement is
	// block: an unapproved high-risk change gates the file pipeline.
	CategoryApproval Category = "approval"
)

// FileChange describes one file mutation in a proposed change.
type FileChange struct {
	Path    string    `json:"path"`
	OldPath string    `json:"old_path,omitempty"` // for renames
	Op      Operation `json:"op"`
	Added   []string  `json:"added,omitempty"`   // added line numbers
	Removed []string  `json:"removed,omitempty"` // removed line numbers
	Content string    `json:"content,omitempty"` // proposed content (pre-write)
	Diff    string    `json:"diff,omitempty"`    // unified diff (staged)
}

// Evidence provides supporting context for a finding (never contains secrets).
type Evidence struct {
	Kind        string `json:"kind"` // e.g. "import-edge", "pattern-match"
	Description string `json:"description"`
	Location    string `json:"location,omitempty"` // file:line or symbol
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

	// Suppression maturity (P1-2): a finding covered by a reviewed, expiring
	// suppression is marked Suppressed, downgraded to INFO (visible, never
	// blocking), and stamped with the suppression reason so the lift stays
	// auditable. Owner is the responsible team from owners.yaml for routing.
	Suppressed        bool   `json:"suppressed,omitempty"`
	SuppressionReason string `json:"suppression_reason,omitempty"`
	Owner             string `json:"owner,omitempty"`

	// Kern 2.0 Evidence provenance (P2-4): additive, omitempty metadata that
	// aligns Blueprint findings with the ecosystem's shared findings format.
	// RuleVersion is the rule-family version ("1" for every v1 check).
	// KernVersion is the kern binary that produced the underlying signal
	// (best-effort; empty when the probe failed or no kern was involved).
	// IndexFreshness is the kern-index state for index-backed checks (P0.2),
	// empty when unknown:
	//   "fresh"   — the index was already current when the check ran (no rebuild);
	//   "rebuilt" — the index was stale, the check rebuilt it, and it is now current;
	//   "stale"   — the index is stale and could not be made current (the check
	//               errored rather than pass on a potentially-misleading index).
	// Confidence is the detector's 0..1 confidence. Scope is "file"
	// when the finding is about one file, "repo" when it is about the whole
	// repository. All fields are stamped by the check that owns them except
	// KernVersion, which the service stamps from its config.
	RuleVersion    string  `json:"rule_version,omitempty"`
	KernVersion    string  `json:"kern_version,omitempty"`
	IndexFreshness string  `json:"index_freshness,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Scope          string  `json:"scope,omitempty"`
}

// CheckResult is the outcome of one named check (e.g. "secret:gitleaks").
type CheckResult struct {
	Name     string    `json:"name"`
	Status   Status    `json:"status"`
	Findings []Finding `json:"findings,omitempty"`
	Duration int64     `json:"duration_ms"`
	Skipped  bool      `json:"skipped,omitempty"`
	Error    string    `json:"error,omitempty"`
	// Source is the change source that produced this result. The service stamps
	// it from ChangeRequest.Source before policy evaluation so per-source
	// overrides (spec P0-3) can be resolved. Empty means the change declared
	// no provenance; global rules apply.
	Source Source `json:"source,omitempty"`
}

// Summary is the count rollup of a validation result.
type Summary struct {
	Total    int `json:"total"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Blocks   int `json:"blocks"`
	Skipped  int `json:"skipped"`
}

// ValidationOutcome (P0.4) summarizes a blueprint validation for kern's
// audit chain: status, exit code, blocked files, correlation id, and finding
// count. Kern consumes it via the `kern audit append` chain link to mark the
// blocked context stale and route follow-ups. The wire format is the UNTAGGED
// exported Go field names ("Status", "ExitCode", "BlockedFiles",
// "CorrelationID", "Findings") — it must round-trip exactly with kern's
// internal/governance/audit.ValidationOutcome, which mirrors the AuditEntry
// convention of exported field names as JSON keys.
type ValidationOutcome struct {
	Status        string   // "PASS" | "WARN" | "BLOCK" | "ERROR" | "SKIP"
	ExitCode      int      // pipeline exit code
	BlockedFiles  []string // unique paths of BLOCK-severity findings
	CorrelationID string   // correlation id of the validation run
	Findings      int      // total finding count
}

// ContextProvenanceSchemaVersion is the version of the retrieval-provenance
// contract Blueprint consumes from kern's MCP tools (kern_explore,
// kern_context, kern_graph). It mirrors the KernContractVersion pattern in
// adapters/kern: Blueprint validates the schema on consumption, but — unlike
// a safety gate — a version skew is only a WARN (provenance is audit
// metadata, never a blocker).
const ContextProvenanceSchemaVersion = 1

// ContextProvenance (P1.2) is the structured retrieval provenance kern's MCP
// tools attach to their results. An agent that made a change decision on top
// of an authorized retrieval echoes result.provenance into the Blueprint
// change payload; Blueprint records it on the audit Record and cites it in
// the kern chain link. The JSON shape must round-trip exactly with kern's
// `provenance` field. Nil when the change carries no provenance (raw or
// ungoverned flows).
type ContextProvenance struct {
	SchemaVersion int `json:"schema_version"`
	// Mode is "governed" when the retrieval ran under an authorizing rule,
	// "raw" when it did not. In raw mode AuthorizingRule is nil.
	Mode string `json:"mode"`
	// AuthorizingRule is the policy rule that authorized the retrieval.
	// Absent in raw mode.
	AuthorizingRule *AuthorizingRule `json:"authorizing_rule,omitempty"`
	// Index is the kern index state the retrieval ran against.
	Index IndexProvenance `json:"index"`
	// Symbols lists the symbols the retrieval returned.
	Symbols []SymbolProvenance `json:"symbols"`
}

// AuthorizingRule is the policy rule that authorized a governed retrieval.
type AuthorizingRule struct {
	PolicySource string `json:"policy_source"`
	Policy       string `json:"policy"`
	Fingerprint  string `json:"fingerprint"`
	DecidedAt    string `json:"decided_at"` // RFC3339
}

// IndexProvenance is the kern index state a retrieval ran against.
type IndexProvenance struct {
	TreeOID          string `json:"tree_oid"`
	ContentRoot      string `json:"content_root"`
	GitCommit        string `json:"git_commit"`
	BuiltAt          string `json:"built_at"`          // RFC3339
	FreshnessVerdict string `json:"freshness_verdict"` // "fresh" | "rebuilt" | "stale"
}

// SymbolProvenance is one symbol a retrieval returned.
type SymbolProvenance struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// ChangeRequest is the normalized input to the validation pipeline (spec Section 5).
type ChangeRequest struct {
	RepositoryRoot string            `json:"repository_root"`
	Source         Source            `json:"source"`
	AgentID        string            `json:"agent_id,omitempty"`
	Operation      Operation         `json:"operation"`
	Files          []FileChange      `json:"files"`
	BaseRevision   string            `json:"base_revision,omitempty"`
	TargetRevision string            `json:"target_revision,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	// ContextProvenance (P1.2) is the retrieval provenance the agent echoes
	// from kern's result.provenance (kern_explore/kern_context/kern_graph).
	// It links this change decision to the context authorization that
	// informed it. Nil when the change carries no provenance.
	ContextProvenance *ContextProvenance `json:"context_provenance,omitempty"`
}

// ValidationResult is the structured output of the validation pipeline (spec Section 5).
type ValidationResult struct {
	Status        Status        `json:"status"`
	ExitCode      int           `json:"exit_code"`
	Findings      []Finding     `json:"findings"`
	Summary       Summary       `json:"summary"`
	Checks        []CheckResult `json:"checks"`
	DurationMs    int64         `json:"duration_ms"`
	CorrelationID string        `json:"correlation_id"`
	// ChecksSkipped lists opt-in checks that were NOT exercised in this run
	// (e.g. ["resilience"] when --resilience was absent). Explicitly recorded
	// so a skipped check is visible audit state (P2-2), never a silent
	// omission. Empty when every registered check ran.
	ChecksSkipped []string `json:"checks_skipped,omitempty"`
}
