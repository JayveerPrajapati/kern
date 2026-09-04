package policy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

const (
	defaultMode           = "enforce"
	defaultTimeoutSec     = 120
	defaultMaxOutputBytes = 200000
	// defaultApprovalMaxDiffLines is the added+removed line threshold above
	// which a diff counts as large for the approval gate (P1.3).
	defaultApprovalMaxDiffLines = 500
	maxTimeoutSec               = 3600
	maxOutputBytes              = 10 * 1024 * 1024 // 10MB
)

// knownCategories maps config-file policy keys to domain categories. The set
// is derived from the domain.Category constants so it cannot drift from the
// enforcement model: a typo'd key (e.g. "architecure") is a hard config error
// instead of a silently-ignored rule that weakens enforcement.
var knownCategories = func() map[string]domain.Category {
	m := make(map[string]domain.Category)
	for _, cat := range []domain.Category{
		domain.CategoryArchitecture,
		domain.CategorySecret,
		domain.CategoryDuplication,
		domain.CategoryTests,
		domain.CategoryBuild,
		domain.CategoryResilience,
		domain.CategoryPolicy,
		domain.CategoryApproval,
	} {
		m[string(cat)] = cat
	}
	// Config-file alias: the domain constant is "secret", but the config key
	// has always been the plural "secrets" (defaults + existing configs).
	m["secrets"] = domain.CategorySecret
	return m
}()

// knownCategoryNames returns the sorted config-file policy keys, for error
// messages.
func knownCategoryNames() []string {
	names := make([]string, 0, len(knownCategories))
	for k := range knownCategories {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// knownSources maps config-file source keys to domain sources. Like
// knownCategories, the set is derived from the domain.Source constants so it
// cannot drift from the provenance model: a typo'd source key (e.g. "depbot")
// is a hard config error instead of a silently-ignored override that weakens
// enforcement for the intended source.
var knownSources = func() map[string]domain.Source {
	m := make(map[string]domain.Source)
	for _, src := range []domain.Source{
		domain.SourceAgent,
		domain.SourceIDE,
		domain.SourceHuman,
		domain.SourceRefactor,
		domain.SourceDepBot,
		domain.SourceCI,
		domain.SourceWatch,
	} {
		m[string(src)] = src
	}
	return m
}()

// knownSourceNames returns the sorted config-file source keys, for error
// messages.
func knownSourceNames() []string {
	names := make([]string, 0, len(knownSources))
	for k := range knownSources {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// enforcementValues maps config-file enforcement strings to domain enforcement.
var enforcementValues = map[string]domain.Enforcement{
	"block": domain.EnforcementBlock,
	"warn":  domain.EnforcementWarn,
	"skip":  domain.EnforcementSkip,
}

// enforcementFromPolicyValue resolves a configured policy value to its
// enforcement string. Both the string shorthand ("block") and the object form
// ({enforcement: block}) are accepted; anything else yields ok=false.
func enforcementFromPolicyValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case map[string]any:
		e, ok := t["enforcement"].(string)
		return e, ok
	case map[any]any:
		e, ok := t["enforcement"].(string)
		return e, ok
	default:
		return "", false
	}
}

// LoadedConfig is the validated, resolved configuration ready for the service.
type LoadedConfig struct {
	File    ConfigFile
	Policy  Policy         // for the policy engine
	Service service.Config // for the BlueprintService
	// Warnings collects non-fatal loader notices (P1-2) — e.g. a suppression
	// or owner referencing a rule no check can produce. Consumers MAY surface
	// them (check prints them to stderr); they never change validation
	// outcome.
	Warnings []string
}

// Load reads and validates .blueprint/config.yaml, .blueprint/suppressions.yaml,
// and .blueprint/owners.yaml from repoRoot. A missing config file yields
// DefaultConfig() with no error (spec: defaults must be conservative); a
// missing suppressions/owners file is simply empty (P1-2). Malformed or
// invalid files are hard errors.
func Load(repoRoot string) (*LoadedConfig, error) {
	cfg, err := loadConfig(repoRoot)
	if err != nil {
		return nil, err
	}

	// P1-2 suppression maturity: suppressions + owner routing are layered on
	// top of the resolved config. Missing file -> empty, no error; malformed
	// or invalid -> hard error, same style as config.yaml validation.
	if err := loadSuppressions(repoRoot, cfg); err != nil {
		return nil, err
	}
	if err := loadOwners(repoRoot, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadConfig reads and validates .blueprint/config.yaml (the original Load
// body, extracted so suppression/owner files can be layered on top).
func loadConfig(repoRoot string) (*LoadedConfig, error) {
	path := filepath.Join(repoRoot, ".blueprint", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var file ConfigFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := validate(&file); err != nil {
		return nil, err
	}

	return build(&file), nil
}

// loadSuppressions reads .blueprint/suppressions.yaml (P1-2) and resolves it
// into Policy.Suppressions. A missing file is empty with no error; a malformed
// file or an invalid entry is a hard error, same style as config.yaml. An
// unknown rule id is a non-fatal warning surfaced via LoadedConfig.Warnings.
func loadSuppressions(repoRoot string, cfg *LoadedConfig) error {
	path := filepath.Join(repoRoot, ".blueprint", "suppressions.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read suppressions %s: %w", path, err)
	}

	var file SuppressionsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse suppressions %s: %w", path, err)
	}

	supps, warns, err := resolveSuppressions(&file)
	if err != nil {
		return err
	}
	cfg.Policy.Suppressions = supps
	cfg.Warnings = append(cfg.Warnings, warns...)
	return nil
}

// resolveSuppressions validates a parsed suppressions file and resolves it
// into the policy's suppression list, collecting non-fatal warnings for rule
// ids no registered check can produce. Hard errors: version != 1, missing or
// empty rule_id, empty reason, empty reviewer, unparsable expires, invalid
// file glob.
func resolveSuppressions(f *SuppressionsFile) ([]Suppression, []string, error) {
	if f.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported suppressions version: %d (expected 1)", f.Version)
	}
	out := make([]Suppression, 0, len(f.Suppressions))
	var warns []string
	for _, e := range f.Suppressions {
		if strings.TrimSpace(e.RuleID) == "" {
			return nil, nil, fmt.Errorf("suppression with missing or empty rule_id")
		}
		if strings.TrimSpace(e.Reason) == "" {
			return nil, nil, fmt.Errorf("suppression for rule %q: empty reason", e.RuleID)
		}
		if strings.TrimSpace(e.Reviewer) == "" {
			return nil, nil, fmt.Errorf("suppression for rule %q: empty reviewer", e.RuleID)
		}
		expires, err := time.Parse("2006-01-02", strings.TrimSpace(e.Expires))
		if err != nil {
			return nil, nil, fmt.Errorf("suppression for rule %q: invalid expires %q: %v (want YYYY-MM-DD)", e.RuleID, e.Expires, err)
		}
		if e.File != "" {
			if _, err := path.Match(e.File, "anything"); err != nil {
				return nil, nil, fmt.Errorf("suppression for rule %q: invalid file glob %q: %v", e.RuleID, e.File, err)
			}
		}
		if !isKnownRuleID(e.RuleID) {
			warns = append(warns, fmt.Sprintf("suppression references unknown rule %q; it will never match", e.RuleID))
		}
		out = append(out, Suppression{
			RuleID:   e.RuleID,
			File:     e.File,
			Reason:   e.Reason,
			Reviewer: e.Reviewer,
			Expires:  expires,
		})
	}
	return out, warns, nil
}

// loadOwners reads .blueprint/owners.yaml (P1-2) and resolves it into
// Policy.Owners. A missing file is empty with no error; a malformed file or an
// invalid entry is a hard error. An unknown rule id is a non-fatal warning.
func loadOwners(repoRoot string, cfg *LoadedConfig) error {
	path := filepath.Join(repoRoot, ".blueprint", "owners.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read owners %s: %w", path, err)
	}

	var file OwnersFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse owners %s: %w", path, err)
	}

	owners, warns, err := resolveOwners(&file)
	if err != nil {
		return err
	}
	cfg.Policy.Owners = owners
	cfg.Warnings = append(cfg.Warnings, warns...)
	return nil
}

// resolveOwners validates a parsed owners file and resolves it into the
// policy's rule-id -> owners map. Hard errors: version != 1, missing or empty
// rule_id, empty owner list, empty owner name.
func resolveOwners(f *OwnersFile) (map[string][]string, []string, error) {
	if f.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported owners version: %d (expected 1)", f.Version)
	}
	out := make(map[string][]string, len(f.Owners))
	var warns []string
	for ruleID, owners := range f.Owners {
		if strings.TrimSpace(ruleID) == "" {
			return nil, nil, fmt.Errorf("owner entry with missing or empty rule_id")
		}
		if len(owners) == 0 {
			return nil, nil, fmt.Errorf("owner for rule %q: empty owner list", ruleID)
		}
		for _, o := range owners {
			if strings.TrimSpace(o) == "" {
				return nil, nil, fmt.Errorf("owner for rule %q: empty owner name", ruleID)
			}
		}
		if !isKnownRuleID(ruleID) {
			warns = append(warns, fmt.Sprintf("owner for unknown rule %q", ruleID))
		}
		out[ruleID] = owners
	}
	return out, warns, nil
}

// knownRuleIDs is the set of literal rule ids emitted by checks whose category
// prefix is not itself an enforcement category (sandbox). Combined with the
// known-category prefix rule and the dynamic secret:* family, it drives the
// best-effort "unknown rule" warning (P1-2) — a warning, never an error,
// because a typo'd rule id must not hard-fail a suppression the operator can
// see in the check output.
var knownRuleIDs = map[string]bool{
	"sandbox:build-failure": true,
	"sandbox:test-failure":  true,
}

// isKnownRuleID reports whether a rule id could be produced by a registered
// check. Rule ids are "<category>:<detail>". The id is known when it is a
// literal known id, a dynamic secret:* id (kern emits secret:<label>, e.g.
// secret:hardcoded-secret), or its category prefix is a known enforcement
// category. An id failing all three can never match a real finding.
func isKnownRuleID(id string) bool {
	if knownRuleIDs[id] {
		return true
	}
	if strings.HasPrefix(id, "secret:") {
		return true // kern emits secret rule ids dynamically
	}
	if i := strings.Index(id, ":"); i > 0 {
		_, ok := knownCategories[id[:i]]
		return ok
	}
	return false
}

// DefaultConfig returns conservative defaults: mode=enforce, architecture=block,
// secrets=block, tests=block, duplication=warn, resilience=warn, timeout=120s,
// max_output=200000.
//
// duplication=warn because the in-house structural pass is advisory-only
// (precision 0.50 / FPR 0.75 — see docs/duplication-benchmark.md): it must
// never gate a change on its own. Under warn enforcement, even a two-pass
// jscpd-confirmed block (duplication:confirmed-block) is reported as WARN by
// default; operators who want jscpd-confirmed duplication to fail the gate
// can set `duplication: block` in .blueprint/config.yaml.
func DefaultConfig() *LoadedConfig {
	file := ConfigFile{
		Version: 1,
		Mode:    defaultMode,
		Policies: map[string]any{
			"architecture": "block",
			"secrets":      "block",
			"duplication":  "warn",
			"tests":        "block",
			"resilience":   "warn",
			"approval":     "block",
		},
		Execution: ExecutionConfig{
			TimeoutSeconds: defaultTimeoutSec,
			MaxOutputBytes: defaultMaxOutputBytes,
		},
		Feedback: FeedbackConfig{Format: "json"},
		Approval: ApprovalConfig{
			Enabled:              boolPtr(true),
			RequireForSources:    []string{string(domain.SourceAgent)},
			RequireForRiskLevels: []string{"high"},
			MaxDiffLines:         defaultApprovalMaxDiffLines,
		},
	}
	return build(&file)
}

// validate checks a parsed ConfigFile, applying defaults and resolving values
// in place. The returned error, if any, means the config is unusable.
func validate(c *ConfigFile) error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version: %d (expected 1)", c.Version)
	}

	if c.Mode == "" {
		c.Mode = defaultMode
	}
	switch c.Mode {
	case "enforce", "warn", "off":
	default:
		return fmt.Errorf("invalid mode %q: must be enforce|warn|off", c.Mode)
	}

	var unknown []string
	for key, val := range c.Policies {
		if _, ok := knownCategories[key]; !ok {
			unknown = append(unknown, key)
			continue
		}
		enf, ok := enforcementFromPolicyValue(val)
		if !ok {
			return fmt.Errorf("invalid enforcement for policy %q: must be block|warn|skip", key)
		}
		if _, ok := enforcementValues[enf]; !ok {
			return fmt.Errorf("invalid enforcement %q for policy %q: must be block|warn|skip", enf, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown policy %q: valid policies are %s", strings.Join(unknown, ", "), strings.Join(knownCategoryNames(), ", "))
	}

	// Per-source overrides (spec P0-3): every source key must be a known
	// source and every category key inside it a known category, with a valid
	// enforcement value — same hard-error style as the top-level policies so a
	// typo cannot silently weaken enforcement for a source.
	var unknownSources []string
	for src, cats := range c.Sources {
		if _, ok := knownSources[src]; !ok {
			unknownSources = append(unknownSources, src)
			continue
		}
		var unknownCats []string
		for cat, val := range cats {
			if _, ok := knownCategories[cat]; !ok {
				unknownCats = append(unknownCats, cat)
				continue
			}
			enf, ok := enforcementFromPolicyValue(val)
			if !ok {
				return fmt.Errorf("invalid enforcement for source %q policy %q: must be block|warn|skip", src, cat)
			}
			if _, ok := enforcementValues[enf]; !ok {
				return fmt.Errorf("invalid enforcement %q for source %q policy %q: must be block|warn|skip", enf, src, cat)
			}
		}
		if len(unknownCats) > 0 {
			sort.Strings(unknownCats)
			return fmt.Errorf("unknown policy %q for source %q: valid policies are %s", strings.Join(unknownCats, ", "), src, strings.Join(knownCategoryNames(), ", "))
		}
	}
	if len(unknownSources) > 0 {
		sort.Strings(unknownSources)
		return fmt.Errorf("unknown source %q: valid sources are %s", strings.Join(unknownSources, ", "), strings.Join(knownSourceNames(), ", "))
	}

	if c.Execution.TimeoutSeconds <= 0 {
		c.Execution.TimeoutSeconds = defaultTimeoutSec
	}
	if c.Execution.TimeoutSeconds > maxTimeoutSec {
		return fmt.Errorf("timeout_seconds %d exceeds maximum %d", c.Execution.TimeoutSeconds, maxTimeoutSec)
	}

	if c.Execution.MaxOutputBytes <= 0 {
		c.Execution.MaxOutputBytes = defaultMaxOutputBytes
	}
	if c.Execution.MaxOutputBytes > maxOutputBytes {
		return fmt.Errorf("max_output_bytes %d exceeds maximum %d", c.Execution.MaxOutputBytes, maxOutputBytes)
	}

	// P2-3 latency budget: 0 disables the gate; a negative value is a
	// configuration bug (a silent disable would hide a misconfiguration the
	// operator can see in the check output), so it is a hard error.
	if c.Execution.StagedLatencyBudgetMs < 0 {
		return fmt.Errorf("staged_latency_budget_ms %d must be >= 0 (0 disables the latency budget)", c.Execution.StagedLatencyBudgetMs)
	}
	// P1.3 approval gate: apply conservative defaults and validate the
	// section. An unset enabled flag means on; unknown sources or risk levels
	// are hard errors (a typo must not silently weaken the two-person rule).
	if c.Approval.Enabled == nil {
		c.Approval.Enabled = boolPtr(true)
	}
	if len(c.Approval.RequireForSources) == 0 {
		c.Approval.RequireForSources = []string{string(domain.SourceAgent)}
	}
	for _, src := range c.Approval.RequireForSources {
		if _, ok := knownSources[src]; !ok {
			return fmt.Errorf("invalid approval require_for_sources %q: valid sources are %s", src, strings.Join(knownSourceNames(), ", "))
		}
	}
	if len(c.Approval.RequireForRiskLevels) == 0 {
		c.Approval.RequireForRiskLevels = []string{"high"}
	}
	for _, lvl := range c.Approval.RequireForRiskLevels {
		switch lvl {
		case "low", "medium", "high":
		default:
			return fmt.Errorf("invalid approval require_for_risk_levels %q: must be low|medium|high", lvl)
		}
	}
	if c.Approval.MaxDiffLines < 0 {
		return fmt.Errorf("approval max_diff_lines %d must be >= 0", c.Approval.MaxDiffLines)
	}
	if c.Approval.MaxDiffLines == 0 {
		c.Approval.MaxDiffLines = defaultApprovalMaxDiffLines
	}

	if c.Sandbox.TimeoutSeconds < 0 {
		return fmt.Errorf("sandbox timeout_seconds %d must be >= 0", c.Sandbox.TimeoutSeconds)
	}
	for i, m := range c.Sandbox.Matrix {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("sandbox matrix[%d] missing name", i)
		}
		if m.Build == "" && m.Test == "" && m.Command == "" {
			return fmt.Errorf("sandbox matrix[%d] (%q) must specify at least one of build, test, or command", i, m.Name)
		}
	}
	return nil
}

// boolPtr returns a pointer to b (for yaml *bool fields where nil means the
// conservative default).
func boolPtr(b bool) *bool { return &b }

// build resolves a validated ConfigFile into a LoadedConfig. Rules start from
// the conservative defaults and are overlaid with configured policies, so an
// empty or partial policies map still yields full coverage.
func build(c *ConfigFile) *LoadedConfig {
	rules := defaultRules()
	for key, val := range c.Policies {
		if enf, ok := enforcementFromPolicyValue(val); ok {
			rules[knownCategories[key]] = enforcementValues[enf]
		}
	}

	// Per-source overrides (spec P0-3), validated in validate().
	sourceRules := make(map[domain.Source]map[domain.Category]domain.Enforcement)
	for src, cats := range c.Sources {
		srcRules := make(map[domain.Category]domain.Enforcement)
		for cat, val := range cats {
			if enf, ok := enforcementFromPolicyValue(val); ok {
				srcRules[knownCategories[cat]] = enforcementValues[enf]
			}
		}
		sourceRules[knownSources[src]] = srcRules
	}

	return &LoadedConfig{
		File: *c,
		Policy: Policy{
			Mode:        c.Mode,
			Rules:       rules,
			SourceRules: sourceRules,
		},
		Service: service.Config{
			Mode:                  c.Mode,
			Enforcement:           rules,
			SourceRules:           sourceRules,
			TimeoutSec:            c.Execution.TimeoutSeconds,
			MaxOutputBytes:        c.Execution.MaxOutputBytes,
			StagedLatencyBudgetMs: c.Execution.StagedLatencyBudgetMs,
		},
	}
}

// defaultRules returns the conservative default enforcement map. Duplication
// is warn-enforced: the in-house structural pass is advisory-only (precision
// 0.50 / FPR 0.75, docs/duplication-benchmark.md) and must not gate a change
// on its own. Repos that want jscpd-confirmed duplication (P1.1 two-pass) to
// fail the gate opt in with `duplication: block` in .blueprint/config.yaml.
func defaultRules() map[domain.Category]domain.Enforcement {
	return map[domain.Category]domain.Enforcement{
		domain.CategoryArchitecture: domain.EnforcementBlock,
		domain.CategorySecret:       domain.EnforcementBlock,
		domain.CategoryDuplication:  domain.EnforcementWarn,
		domain.CategoryTests:        domain.EnforcementBlock,
		domain.CategoryResilience:   domain.EnforcementWarn,
		domain.CategoryApproval:     domain.EnforcementBlock,
	}
}
