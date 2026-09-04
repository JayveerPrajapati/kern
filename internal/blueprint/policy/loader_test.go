package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// writeConfig creates a temp repo root with the given .blueprint/config.yaml content.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".blueprint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const fullConfig = `version: 1

mode: enforce

policies:
  architecture: block
  secrets: block
  duplication: warn
  tests: block
  resilience: warn

thresholds:
  duplication_warn: 0.85
  duplication_block: 0.95

execution:
  timeout_seconds: 120
  max_output_bytes: 200000

feedback:
  format: json
  include_suggestions: true
`

func TestLoadMissingConfigReturnsDefaults(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(missing config) error = %v, want nil", err)
	}
	want := DefaultConfig()
	if got.File.Version != want.File.Version || got.File.Mode != want.File.Mode {
		t.Errorf("File = %+v, want %+v", got.File, want.File)
	}
	if got.Policy.Mode != want.Policy.Mode {
		t.Errorf("Policy.Mode = %q, want %q", got.Policy.Mode, want.Policy.Mode)
	}
	for cat, enf := range want.Policy.Rules {
		if got.Policy.Rules[cat] != enf {
			t.Errorf("Policy.Rules[%q] = %q, want %q", cat, got.Policy.Rules[cat], enf)
		}
	}
	if got.Service.Mode != want.Service.Mode ||
		got.Service.TimeoutSec != want.Service.TimeoutSec ||
		got.Service.MaxOutputBytes != want.Service.MaxOutputBytes {
		t.Errorf("Service = %+v, want %+v", got.Service, want.Service)
	}
}

func TestLoadValidFullConfig(t *testing.T) {
	got, err := Load(writeConfig(t, fullConfig))
	if err != nil {
		t.Fatalf("Load(valid config) error = %v, want nil", err)
	}

	if got.File.Version != 1 {
		t.Errorf("File.Version = %d, want 1", got.File.Version)
	}
	if got.File.Mode != "enforce" {
		t.Errorf("File.Mode = %q, want enforce", got.File.Mode)
	}
	if len(got.File.Policies) != 5 {
		t.Errorf("File.Policies = %v, want 5 entries", got.File.Policies)
	}
	if got.File.Thresholds["duplication_warn"] != 0.85 || got.File.Thresholds["duplication_block"] != 0.95 {
		t.Errorf("File.Thresholds = %v, want duplication_warn=0.85 duplication_block=0.95", got.File.Thresholds)
	}
	if got.File.Execution.TimeoutSeconds != 120 {
		t.Errorf("File.Execution.TimeoutSeconds = %d, want 120", got.File.Execution.TimeoutSeconds)
	}
	if got.File.Execution.MaxOutputBytes != 200000 {
		t.Errorf("File.Execution.MaxOutputBytes = %d, want 200000", got.File.Execution.MaxOutputBytes)
	}
	if got.File.Feedback.Format != "json" {
		t.Errorf("File.Feedback.Format = %q, want json", got.File.Feedback.Format)
	}
	if !got.File.Feedback.IncludeSuggestions {
		t.Errorf("File.Feedback.IncludeSuggestions = false, want true")
	}

	if got.Policy.Mode != "enforce" {
		t.Errorf("Policy.Mode = %q, want enforce", got.Policy.Mode)
	}
	wantRules := map[domain.Category]domain.Enforcement{
		domain.CategoryArchitecture: domain.EnforcementBlock,
		domain.CategorySecret:       domain.EnforcementBlock,
		domain.CategoryDuplication:  domain.EnforcementWarn,
		domain.CategoryTests:        domain.EnforcementBlock,
		domain.CategoryResilience:   domain.EnforcementWarn,
	}
	for cat, enf := range wantRules {
		if got.Policy.Rules[cat] != enf {
			t.Errorf("Policy.Rules[%q] = %q, want %q", cat, got.Policy.Rules[cat], enf)
		}
	}

	if got.Service.Mode != "enforce" {
		t.Errorf("Service.Mode = %q, want enforce", got.Service.Mode)
	}
	if got.Service.Enforcement[domain.CategoryArchitecture] != domain.EnforcementBlock {
		t.Errorf("Service.Enforcement[architecture] = %q, want block", got.Service.Enforcement[domain.CategoryArchitecture])
	}
	if got.Service.TimeoutSec != 120 {
		t.Errorf("Service.TimeoutSec = %d, want 120", got.Service.TimeoutSec)
	}
	if got.Service.MaxOutputBytes != 200000 {
		t.Errorf("Service.MaxOutputBytes = %d, want 200000", got.Service.MaxOutputBytes)
	}
}

func TestLoadUnsupportedVersion(t *testing.T) {
	_, err := Load(writeConfig(t, "version: 2\nmode: enforce\n"))
	if err == nil {
		t.Fatal("Load(version 2) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported config version") {
		t.Errorf("error = %q, want mention of unsupported config version", err)
	}
}

func TestLoadInvalidMode(t *testing.T) {
	_, err := Load(writeConfig(t, "version: 1\nmode: bogus\n"))
	if err == nil {
		t.Fatal("Load(mode bogus) error = nil, want error")
	}
}

func TestLoadInvalidEnforcement(t *testing.T) {
	cfg := "version: 1\nmode: enforce\npolicies:\n  architecture: bogus\n"
	_, err := Load(writeConfig(t, cfg))
	if err == nil {
		t.Fatal("Load(bogus enforcement) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid enforcement") {
		t.Errorf("error = %q, want mention of invalid enforcement", err)
	}
}

func TestLoadUnknownPolicy(t *testing.T) {
	cfg := "version: 1\nmode: enforce\npolicies:\n  unknown: block\n"
	_, err := Load(writeConfig(t, cfg))
	if err == nil {
		t.Fatal("Load(unknown policy) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown policy") {
		t.Errorf("error = %q, want mention of unknown policy", err)
	}
}

func TestLoadEmptyPoliciesApplyDefaults(t *testing.T) {
	got, err := Load(writeConfig(t, "version: 1\nmode: enforce\n"))
	if err != nil {
		t.Fatalf("Load(empty policies) error = %v, want nil", err)
	}

	want := map[domain.Category]domain.Enforcement{
		domain.CategoryArchitecture: domain.EnforcementBlock,
		domain.CategorySecret:       domain.EnforcementBlock,
		domain.CategoryDuplication:  domain.EnforcementWarn, // advisory-only in-house pass (P1.5); opt into block via config
		domain.CategoryTests:        domain.EnforcementBlock,
		domain.CategoryResilience:   domain.EnforcementWarn,
	}
	for cat, enf := range want {
		if got.Policy.Rules[cat] != enf {
			t.Errorf("Policy.Rules[%q] = %q, want %q", cat, got.Policy.Rules[cat], enf)
		}
		if got.Service.Enforcement[cat] != enf {
			t.Errorf("Service.Enforcement[%q] = %q, want %q", cat, got.Service.Enforcement[cat], enf)
		}
	}
}

func TestLoadTimeoutZeroDefaultsTo120(t *testing.T) {
	cfg := "version: 1\nmode: enforce\nexecution:\n  timeout_seconds: 0\n"
	got, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load(timeout 0) error = %v, want nil", err)
	}
	if got.Service.TimeoutSec != 120 {
		t.Errorf("Service.TimeoutSec = %d, want 120", got.Service.TimeoutSec)
	}
	if got.File.Execution.TimeoutSeconds != 120 {
		t.Errorf("File.Execution.TimeoutSeconds = %d, want 120", got.File.Execution.TimeoutSeconds)
	}
}

func TestLoadTimeoutTooLarge(t *testing.T) {
	cfg := "version: 1\nmode: enforce\nexecution:\n  timeout_seconds: 5000\n"
	_, err := Load(writeConfig(t, cfg))
	if err == nil {
		t.Fatal("Load(timeout 5000) error = nil, want error")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	_, err := Load(writeConfig(t, "version: [unclosed\n  mode: enforce\n"))
	if err == nil {
		t.Fatal("Load(malformed YAML) error = nil, want error")
	}
}

// TestLoadUnknownCategoryRejected: a typo'd policy category (both the string
// shorthand and the {enforcement: block} object form) must be rejected with an
// error naming the unknown category — a silently-ignored rule would weaken
// enforcement.
func TestLoadUnknownCategoryRejected(t *testing.T) {
	// Object form: policies: {nonsense: {enforcement: block}}.
	cfg := "version: 1\nmode: enforce\npolicies:\n  nonsense:\n    enforcement: block\n"
	_, err := Load(writeConfig(t, cfg))
	if err == nil {
		t.Fatal("Load(unknown category object form) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error = %q, want mention of the unknown category \"nonsense\"", err)
	}

	// String shorthand must be rejected identically.
	_, err = Load(writeConfig(t, "version: 1\nmode: enforce\npolicies:\n  architecure: block\n"))
	if err == nil {
		t.Fatal("Load(typo'd category string form) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "architecure") {
		t.Errorf("error = %q, want mention of the typo'd category \"architecure\"", err)
	}
}

// TestLoadPolicyObjectForm: the {enforcement: block} object form for a known
// category is accepted and resolved like the string shorthand.
func TestLoadPolicyObjectForm(t *testing.T) {
	cfg := "version: 1\nmode: enforce\npolicies:\n  architecture:\n    enforcement: block\n  secrets: block\n"
	got, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load(object-form policy) error = %v, want nil", err)
	}
	if got.Policy.Rules[domain.CategoryArchitecture] != domain.EnforcementBlock {
		t.Errorf("Policy.Rules[architecture] = %q, want block", got.Policy.Rules[domain.CategoryArchitecture])
	}
	if got.Policy.Rules[domain.CategorySecret] != domain.EnforcementBlock {
		t.Errorf("Policy.Rules[secrets] = %q, want block", got.Policy.Rules[domain.CategorySecret])
	}
}

// TestG16_LoaderSourceValidation: the sources section (spec P0-3) must be
// validated with the same hard-error style as top-level policies, and must
// resolve into both Policy.SourceRules and Service.SourceRules.
func TestG16_LoaderSourceValidation(t *testing.T) {
	// A valid sources section loads and resolves per-source overrides.
	cfg := "version: 1\nmode: enforce\npolicies:\n  duplication: warn\nsources:\n  agent:\n    duplication: skip\n  dep-bot:\n    duplication: skip\n"
	got, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load(valid sources) error = %v, want nil", err)
	}
	if got.Policy.SourceRules[domain.SourceAgent][domain.CategoryDuplication] != domain.EnforcementSkip {
		t.Errorf("Policy.SourceRules[agent][duplication] = %q, want skip", got.Policy.SourceRules[domain.SourceAgent][domain.CategoryDuplication])
	}
	if got.Policy.SourceRules[domain.SourceDepBot][domain.CategoryDuplication] != domain.EnforcementSkip {
		t.Errorf("Policy.SourceRules[dep-bot][duplication] = %q, want skip", got.Policy.SourceRules[domain.SourceDepBot][domain.CategoryDuplication])
	}
	if got.Policy.SourceRules[domain.SourceHuman][domain.CategoryDuplication] != "" {
		t.Errorf("Policy.SourceRules[human][duplication] = %q, want empty (no override)", got.Policy.SourceRules[domain.SourceHuman][domain.CategoryDuplication])
	}
	if got.Service.SourceRules[domain.SourceAgent][domain.CategoryDuplication] != domain.EnforcementSkip {
		t.Errorf("Service.SourceRules[agent][duplication] = %q, want skip", got.Service.SourceRules[domain.SourceAgent][domain.CategoryDuplication])
	}

	// Unknown source key → hard error naming the source.
	_, err = Load(writeConfig(t, "version: 1\nmode: enforce\nsources:\n  alien:\n    duplication: skip\n"))
	if err == nil {
		t.Fatal("Load(unknown source) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "alien") {
		t.Errorf("error = %q, want mention of the unknown source \"alien\"", err)
	}

	// Unknown category key inside a source → hard error naming the category.
	_, err = Load(writeConfig(t, "version: 1\nmode: enforce\nsources:\n  agent:\n    architecure: block\n"))
	if err == nil {
		t.Fatal("Load(unknown category in source) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "architecure") {
		t.Errorf("error = %q, want mention of the unknown category \"architecure\"", err)
	}

	// Invalid enforcement value inside a source → hard error.
	_, err = Load(writeConfig(t, "version: 1\nmode: enforce\nsources:\n  agent:\n    duplication: bogus\n"))
	if err == nil {
		t.Fatal("Load(invalid source enforcement) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid enforcement") {
		t.Errorf("error = %q, want mention of invalid enforcement", err)
	}

	// Object form {enforcement: warn} inside a source is accepted.
	got, err = Load(writeConfig(t, "version: 1\nmode: enforce\nsources:\n  agent:\n    duplication:\n      enforcement: warn\n"))
	if err != nil {
		t.Fatalf("Load(source object form) error = %v, want nil", err)
	}
	if got.Policy.SourceRules[domain.SourceAgent][domain.CategoryDuplication] != domain.EnforcementWarn {
		t.Errorf("Policy.SourceRules[agent][duplication] = %q, want warn (object form)", got.Policy.SourceRules[domain.SourceAgent][domain.CategoryDuplication])
	}

	// Missing config file → defaults with empty SourceRules.
	got, err = Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(missing config) error = %v, want nil", err)
	}
	if len(got.Policy.SourceRules) != 0 {
		t.Errorf("default Policy.SourceRules = %v, want empty", got.Policy.SourceRules)
	}
	if len(got.Service.SourceRules) != 0 {
		t.Errorf("default Service.SourceRules = %v, want empty", got.Service.SourceRules)
	}
}

// --- G20 gate: suppression maturity loader (P1-2) ---

// writeSuppressionFiles creates a temp repo root containing the given
// .blueprint/suppressions.yaml and .blueprint/owners.yaml content. An empty
// string means that file is not written.
func writeSuppressionFiles(t *testing.T, suppressions, owners string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".blueprint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if suppressions != "" {
		if err := os.WriteFile(filepath.Join(dir, "suppressions.yaml"), []byte(suppressions), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if owners != "" {
		if err := os.WriteFile(filepath.Join(dir, "owners.yaml"), []byte(owners), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestG20_LoaderSuppressions: valid suppressions/owners files load; fields
// parse; the expiry parses to the right date; an unknown rule id yields a
// warning, not an error.
func TestG20_LoaderSuppressions(t *testing.T) {
	supp := `version: 1
suppressions:
  - rule_id: secret:hardcoded-secret
    file: "testdata/*"
    reason: "placeholder credentials in test fixtures"
    reviewer: platform-eng
    expires: "2026-12-31"
`
	owners := `version: 1
owners:
  secret:hardcoded-secret: [platform-eng]
`
	got, err := Load(writeSuppressionFiles(t, supp, owners))
	if err != nil {
		t.Fatalf("Load(valid suppressions) error = %v, want nil", err)
	}
	if len(got.Policy.Suppressions) != 1 {
		t.Fatalf("Policy.Suppressions = %d entries, want 1", len(got.Policy.Suppressions))
	}
	s := got.Policy.Suppressions[0]
	if s.RuleID != "secret:hardcoded-secret" || s.File != "testdata/*" ||
		s.Reason != "placeholder credentials in test fixtures" || s.Reviewer != "platform-eng" {
		t.Errorf("Suppression = %+v, want parsed rule/file/reason/reviewer fields", s)
	}
	wantExp, _ := time.Parse("2006-01-02", "2026-12-31")
	if !s.Expires.Equal(wantExp) {
		t.Errorf("Expires = %v, want %v (2026-12-31)", s.Expires, wantExp)
	}
	if got.Policy.Owners["secret:hardcoded-secret"][0] != "platform-eng" {
		t.Errorf("Policy.Owners = %v, want secret:hardcoded-secret: [platform-eng]", got.Policy.Owners)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty (secret:* is a known rule family)", got.Warnings)
	}

	// Unknown rule id (unknown category prefix) => warning, not error.
	got2, err := Load(writeSuppressionFiles(t, `version: 1
suppressions:
  - rule_id: nonsense:rule
    reason: "r"
    reviewer: rev
    expires: "2030-01-01"
`, ""))
	if err != nil {
		t.Fatalf("Load(unknown-rule suppression) error = %v, want nil", err)
	}
	if len(got2.Warnings) == 0 {
		t.Fatal("Warnings empty, want unknown-rule warning")
	}
	found := false
	for _, w := range got2.Warnings {
		if strings.Contains(w, "nonsense:rule") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want mention of nonsense:rule", got2.Warnings)
	}
}

func TestG20_LoaderBadExpiry(t *testing.T) {
	supp := `version: 1
suppressions:
  - rule_id: secret:hardcoded-secret
    reason: "r"
    reviewer: rev
    expires: "not-a-date"
`
	_, err := Load(writeSuppressionFiles(t, supp, ""))
	if err == nil {
		t.Fatal("Load(bad expiry) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "expires") {
		t.Errorf("error = %q, want mention of expires", err)
	}
}

func TestG20_LoaderMissingReviewer(t *testing.T) {
	supp := `version: 1
suppressions:
  - rule_id: secret:hardcoded-secret
    reason: "r"
    expires: "2030-01-01"
`
	_, err := Load(writeSuppressionFiles(t, supp, ""))
	if err == nil {
		t.Fatal("Load(missing reviewer) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Errorf("error = %q, want mention of reviewer", err)
	}
}

func TestG20_LoaderBadGlob(t *testing.T) {
	supp := `version: 1
suppressions:
  - rule_id: secret:hardcoded-secret
    file: "[unclosed"
    reason: "r"
    reviewer: rev
    expires: "2030-01-01"
`
	_, err := Load(writeSuppressionFiles(t, supp, ""))
	if err == nil {
		t.Fatal("Load(bad glob) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "file") {
		t.Errorf("error = %q, want mention of the file glob", err)
	}
}

func TestG20_LoaderMissingFiles(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(no suppression files) error = %v, want nil", err)
	}
	if len(got.Policy.Suppressions) != 0 {
		t.Errorf("Policy.Suppressions = %v, want empty", got.Policy.Suppressions)
	}
	if len(got.Policy.Owners) != 0 {
		t.Errorf("Policy.Owners = %v, want empty", got.Policy.Owners)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", got.Warnings)
	}
}

func TestG20_LoaderOwners(t *testing.T) {
	owners := `version: 1
owners:
  secret:hardcoded-secret: [platform-eng, sec-review]
  architecture:boundary-violation: [core]
`
	got, err := Load(writeSuppressionFiles(t, "", owners))
	if err != nil {
		t.Fatalf("Load(owners) error = %v, want nil", err)
	}
	if len(got.Policy.Owners) != 2 {
		t.Fatalf("Policy.Owners = %d entries, want 2", len(got.Policy.Owners))
	}
	want := []string{"platform-eng", "sec-review"}
	gotOwners := got.Policy.Owners["secret:hardcoded-secret"]
	if len(gotOwners) != len(want) {
		t.Fatalf("Owners[secret:hardcoded-secret] = %v, want %v", gotOwners, want)
	}
	for i := range want {
		if gotOwners[i] != want[i] {
			t.Errorf("Owners[secret:hardcoded-secret][%d] = %q, want %q", i, gotOwners[i], want[i])
		}
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty (all rules known)", got.Warnings)
	}

	// Unknown rule id in owners => warning, not error.
	got2, err := Load(writeSuppressionFiles(t, "", "version: 1\nowners:\n  nonsense:rule: [team]\n"))
	if err != nil {
		t.Fatalf("Load(unknown-rule owner) error = %v, want nil", err)
	}
	found := false
	for _, w := range got2.Warnings {
		if strings.Contains(w, "nonsense:rule") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want owner warning for nonsense:rule", got2.Warnings)
	}
}

func TestG20_LoaderBadVersion(t *testing.T) {
	supp := "version: 2\nsuppressions:\n  - rule_id: secret:hardcoded-secret\n    reason: r\n    reviewer: rev\n    expires: \"2030-01-01\"\n"
	_, err := Load(writeSuppressionFiles(t, supp, ""))
	if err == nil {
		t.Fatal("Load(suppressions version 2) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %q, want mention of version", err)
	}

	_, err = Load(writeSuppressionFiles(t, "", "version: 2\nowners:\n  secret:hardcoded-secret: [platform-eng]\n"))
	if err == nil {
		t.Fatal("Load(owners version 2) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %q, want mention of version", err)
	}
}

func TestG20_LoaderMalformedSuppressionsYAML(t *testing.T) {
	_, err := Load(writeSuppressionFiles(t, "version: [unclosed\n  suppressions: []\n", ""))
	if err == nil {
		t.Fatal("Load(malformed suppressions YAML) error = nil, want error")
	}
}

// --- P2-3 (G24): staged latency budget gate ---

func TestG24_LatencyBudgetParsedIntoServiceConfig(t *testing.T) {
	cfg := "version: 1\nmode: enforce\nexecution:\n  staged_latency_budget_ms: 1500\n"
	got, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load(latency budget 1500) error = %v, want nil", err)
	}
	if got.File.Execution.StagedLatencyBudgetMs != 1500 {
		t.Errorf("File.Execution.StagedLatencyBudgetMs = %d, want 1500", got.File.Execution.StagedLatencyBudgetMs)
	}
	if got.Service.StagedLatencyBudgetMs != 1500 {
		t.Errorf("Service.StagedLatencyBudgetMs = %d, want 1500", got.Service.StagedLatencyBudgetMs)
	}
}

func TestG24_LatencyBudgetNegativeHardError(t *testing.T) {
	cfg := "version: 1\nmode: enforce\nexecution:\n  staged_latency_budget_ms: -5\n"
	_, err := Load(writeConfig(t, cfg))
	if err == nil {
		t.Fatal("Load(negative latency budget) error = nil, want hard config error")
	}
	if !strings.Contains(err.Error(), "staged_latency_budget_ms") {
		t.Errorf("error = %q, want mention of staged_latency_budget_ms", err)
	}
}

func TestG24_LatencyBudgetDefaultDisabled(t *testing.T) {
	got, err := Load(writeConfig(t, "version: 1\nmode: enforce\n"))
	if err != nil {
		t.Fatalf("Load(default) error = %v, want nil", err)
	}
	if got.Service.StagedLatencyBudgetMs != 0 {
		t.Errorf("Service.StagedLatencyBudgetMs = %d, want 0 (disabled)", got.Service.StagedLatencyBudgetMs)
	}
}

func TestLoadApprovalDefaults(t *testing.T) {
	// A missing approval section yields the conservative defaults: enabled,
	// agent source, high risk only, 500-line threshold — and the approval
	// category enforces as BLOCK.
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := got.File.Approval
	if !a.IsEnabled() {
		t.Error("approval must default to enabled")
	}
	if len(a.RequireForSources) != 1 || a.RequireForSources[0] != "agent" {
		t.Errorf("RequireForSources = %v, want [agent]", a.RequireForSources)
	}
	if len(a.RequireForRiskLevels) != 1 || a.RequireForRiskLevels[0] != "high" {
		t.Errorf("RequireForRiskLevels = %v, want [high]", a.RequireForRiskLevels)
	}
	if a.MaxDiffLines != 500 {
		t.Errorf("MaxDiffLines = %d, want 500", a.MaxDiffLines)
	}
	if got.Policy.Rules[domain.CategoryApproval] != domain.EnforcementBlock {
		t.Errorf("approval category enforcement = %q, want block", got.Policy.Rules[domain.CategoryApproval])
	}
}

func TestLoadApprovalExplicitConfig(t *testing.T) {
	root := writeConfig(t, `version: 1
approval:
  enabled: false
  require_for_sources: [ci]
  require_for_risk_levels: [high, medium]
  sensitive_paths: [".kern/**", "deploy/**"]
  max_diff_lines: 100
`)
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := got.File.Approval
	if a.IsEnabled() {
		t.Error("approval enabled: false must be respected")
	}
	if len(a.RequireForSources) != 1 || a.RequireForSources[0] != "ci" {
		t.Errorf("RequireForSources = %v, want [ci]", a.RequireForSources)
	}
	if len(a.RequireForRiskLevels) != 2 || a.RequireForRiskLevels[1] != "medium" {
		t.Errorf("RequireForRiskLevels = %v, want [high medium]", a.RequireForRiskLevels)
	}
	if len(a.SensitivePaths) != 2 || a.SensitivePaths[1] != "deploy/**" {
		t.Errorf("SensitivePaths = %v, want [.kern/** deploy/**]", a.SensitivePaths)
	}
	if a.MaxDiffLines != 100 {
		t.Errorf("MaxDiffLines = %d, want 100", a.MaxDiffLines)
	}
}

func TestLoadApprovalPartialConfigFillsDefaults(t *testing.T) {
	// Only max_diff_lines given: the rest stays conservative defaults.
	root := writeConfig(t, `version: 1
approval:
  max_diff_lines: 1000
`)
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := got.File.Approval
	if !a.IsEnabled() {
		t.Error("approval must default to enabled when not specified")
	}
	if len(a.RequireForSources) != 1 || a.RequireForSources[0] != "agent" {
		t.Errorf("RequireForSources = %v, want default [agent]", a.RequireForSources)
	}
	if a.MaxDiffLines != 1000 {
		t.Errorf("MaxDiffLines = %d, want 1000", a.MaxDiffLines)
	}
}

func TestLoadApprovalInvalidRiskLevel(t *testing.T) {
	root := writeConfig(t, `version: 1
approval:
  require_for_risk_levels: [critical]
`)
	if _, err := Load(root); err == nil {
		t.Fatal("Load with invalid risk level must error")
	}
}

func TestLoadApprovalInvalidSource(t *testing.T) {
	root := writeConfig(t, `version: 1
approval:
  require_for_sources: [depbot]
`)
	if _, err := Load(root); err == nil {
		t.Fatal("Load with unknown source must error")
	}
}

func TestLoadApprovalNegativeMaxDiff(t *testing.T) {
	root := writeConfig(t, `version: 1
approval:
  max_diff_lines: -1
`)
	if _, err := Load(root); err == nil {
		t.Fatal("Load with negative max_diff_lines must error")
	}
}

// TestLoadSandboxMatrix verifies sandbox.matrix parsing and validation.
func TestLoadSandboxMatrix(t *testing.T) {
	t.Run("valid matrix", func(t *testing.T) {
		root := writeConfig(t, `
version: 1
sandbox:
  timeout_seconds: 60
  matrix:
    - name: go
      dir: .
      build: go build ./...
      test: go test ./...
    - name: python
      dir: services/ml
      command: python -m pytest
`)
		cfg, err := Load(root)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if len(cfg.File.Sandbox.Matrix) != 2 {
			t.Fatalf("expected 2 matrix entries, got %d", len(cfg.File.Sandbox.Matrix))
		}
		if cfg.File.Sandbox.Matrix[0].Name != "go" {
			t.Errorf("expected matrix[0].name=go, got %q", cfg.File.Sandbox.Matrix[0].Name)
		}
		if cfg.File.Sandbox.Matrix[1].Name != "python" {
			t.Errorf("expected matrix[1].name=python, got %q", cfg.File.Sandbox.Matrix[1].Name)
		}
		if cfg.File.Sandbox.TimeoutSeconds != 60 {
			t.Errorf("expected sandbox timeout=60, got %d", cfg.File.Sandbox.TimeoutSeconds)
		}
	})

	t.Run("missing name rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `version: 1
sandbox:
  matrix:
    - dir: .
      build: go build ./...
`
		if err := os.WriteFile(filepath.Join(dir, ".blueprint", "config.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(dir)
		if err == nil {
			t.Fatal("expected error for matrix entry with missing name")
		}
		if !strings.Contains(err.Error(), "missing name") {
			t.Errorf("expected 'missing name' in error, got: %v", err)
		}
	})

	t.Run("no command rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `version: 1
sandbox:
  matrix:
    - name: empty
      dir: .
`
		if err := os.WriteFile(filepath.Join(dir, ".blueprint", "config.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(dir)
		if err == nil {
			t.Fatal("expected error for matrix entry with no commands")
		}
		if !strings.Contains(err.Error(), "build, test, or command") {
			t.Errorf("expected 'build, test, or command' in error, got: %v", err)
		}
	})

	t.Run("negative timeout rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `version: 1
sandbox:
  timeout_seconds: -1
`
		if err := os.WriteFile(filepath.Join(dir, ".blueprint", "config.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(dir)
		if err == nil {
			t.Fatal("expected error for negative sandbox timeout")
		}
	})
}
