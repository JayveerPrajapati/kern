package policy

import "time"

// ConfigFile is the raw YAML structure of .blueprint/config.yaml (spec Section 7).
type ConfigFile struct {
	Version    int                       `yaml:"version"`
	Mode       string                    `yaml:"mode"`
	Policies   map[string]any            `yaml:"policies"`
	Sources    map[string]map[string]any `yaml:"sources"`
	Thresholds map[string]float64        `yaml:"thresholds"`
	Execution  ExecutionConfig           `yaml:"execution"`
	Feedback   FeedbackConfig            `yaml:"feedback"`
	// Approval controls the two-person approval gate (P1.3). Absent (or an
	// empty approval: section) means the conservative defaults apply: gate
	// enabled, high-risk agent changes require a human-approved request.
	Approval ApprovalConfig `yaml:"approval"`
	// Sandbox configures isolated build/test execution, including polyglot matrices.
	Sandbox SandboxConfig `yaml:"sandbox"`
}

// SandboxConfig is the .blueprint/config.yaml sandbox section.
type SandboxConfig struct {
	TimeoutSeconds int           `yaml:"timeout_seconds"`
	Matrix         []MatrixEntry `yaml:"matrix"`
}

// MatrixEntry defines one target component in a polyglot build/test matrix.
type MatrixEntry struct {
	Name    string `yaml:"name"`
	Dir     string `yaml:"dir"`     // directory relative to repo root, default "."
	Build   string `yaml:"build"`   // build command string (e.g. "go build ./..." or "npm run build")
	Test    string `yaml:"test"`    // test command string (e.g. "go test ./..." or "npm test")
	Command string `yaml:"command"` // combined command string if build/test is not split
}

// ApprovalConfig is the .blueprint/config.yaml approval section (P1.3). It
// tunes the two-person rule: which sources and risk levels require approval,
// and the sensitive-path / diff-size classification inputs.
type ApprovalConfig struct {
	// Enabled defaults to true (conservative). Use `enabled: false` to turn
	// the gate off entirely — the approval check is then not wired into the
	// pipeline. A *bool distinguishes "unset" from an explicit false.
	Enabled *bool `yaml:"enabled"`
	// RequireForSources lists the change sources the gate applies to. An
	// empty list falls back to the default ["agent"]: humans are the
	// approvers, so they never approve their own change by default.
	RequireForSources []string `yaml:"require_for_sources"`
	// RequireForRiskLevels lists the risk levels that need approval. Empty
	// falls back to ["high"].
	RequireForRiskLevels []string `yaml:"require_for_risk_levels"`
	// SensitivePaths are glob patterns (path.Match plus "**") matched against
	// change file paths. Empty falls back to the built-in list (.kern/,
	// *.pem, *.key, auth/, credentials*, secrets*).
	SensitivePaths []string `yaml:"sensitive_paths"`
	// MaxDiffLines is the added+removed line threshold above which a diff is
	// large. 0 falls back to the default 500.
	MaxDiffLines int `yaml:"max_diff_lines"`
}

// IsEnabled reports whether the gate is on: unset means enabled.
func (a ApprovalConfig) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// ExecutionConfig holds execution limits from the config file.
type ExecutionConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxOutputBytes int `yaml:"max_output_bytes"`
	// StagedLatencyBudgetMs is the P2-3 latency budget for staged validation,
	// in milliseconds. 0 (the default) disables the budget gate; a negative
	// value is a hard config error.
	StagedLatencyBudgetMs int `yaml:"staged_latency_budget_ms"`
}

// FeedbackConfig controls feedback output formatting.
type FeedbackConfig struct {
	Format             string `yaml:"format"`
	IncludeSuggestions bool   `yaml:"include_suggestions"`
}

// Suppression is one reviewed, expiring rule-file suppression (P1-2), decoded
// from .blueprint/suppressions.yaml. A suppression is the only sanctioned way
// to lift a BLOCK: it requires a reviewer and an expiry, and the suppression
// itself stays visible in results and audit records.
type Suppression struct {
	RuleID   string    `yaml:"rule_id"`
	File     string    `yaml:"file"` // optional; empty = all files; path.Match semantics
	Reason   string    `yaml:"reason"`
	Reviewer string    `yaml:"reviewer"`
	Expires  time.Time `yaml:"expires"` // date-only, parsed with time.Parse("2006-01-02", ...)
}

// SuppressionsFile is the raw YAML structure of .blueprint/suppressions.yaml
// (P1-2). Entries are decoded via SuppressionEntry because yaml.v3 cannot
// decode a quoted date-only string into time.Time.
type SuppressionsFile struct {
	Version      int                `yaml:"version"`
	Suppressions []SuppressionEntry `yaml:"suppressions"`
}

// SuppressionEntry mirrors one suppressions.yaml entry with the expires date
// as a raw string; the loader parses and validates it into Suppression.Expires.
type SuppressionEntry struct {
	RuleID   string `yaml:"rule_id"`
	File     string `yaml:"file"`
	Reason   string `yaml:"reason"`
	Reviewer string `yaml:"reviewer"`
	Expires  string `yaml:"expires"`
}

// OwnersFile is the raw YAML structure of .blueprint/owners.yaml (P1-2).
type OwnersFile struct {
	Version int                 `yaml:"version"`
	Owners  map[string][]string `yaml:"owners"` // rule_id -> owner list
}
