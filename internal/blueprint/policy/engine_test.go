package policy

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// G16 gate: source-aware policy (P0-3). Engine tests — these must not depend
// on kern at all.

func TestG16_SourceOverrideChangesStatus(t *testing.T) {
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategoryDuplication: domain.EnforcementWarn,
		},
		SourceRules: map[domain.Source]map[domain.Category]domain.Enforcement{
			domain.SourceAgent: {domain.CategoryDuplication: domain.EnforcementSkip},
		},
	}
	e := NewEngine(p)

	finding := domain.Finding{
		RuleID:   "dup:similar",
		Severity: domain.SeverityWarn,
		Category: domain.CategoryDuplication,
		File:     "a.go",
		Message:  "duplication finding",
	}
	result := domain.CheckResult{
		Name:     "duplication:check",
		Status:   domain.StatusWarn,
		Findings: []domain.Finding{finding},
		Source:   domain.SourceAgent,
	}

	// Agent has an override to skip → StatusSkip.
	status, _ := e.Evaluate(result)
	if status != domain.StatusSkip {
		t.Errorf("Evaluate(agent, skip override) status = %q, want %q", status, domain.StatusSkip)
	}

	// Human has no override → global warn rule → StatusWarn.
	result.Source = domain.SourceHuman
	status, _ = e.Evaluate(result)
	if status != domain.StatusWarn {
		t.Errorf("Evaluate(human, global warn) status = %q, want %q", status, domain.StatusWarn)
	}
}

func TestG16_SourceOverrideNeverPassesBlock(t *testing.T) {
	block := domain.Finding{
		RuleID:   "arch:guard",
		Severity: domain.SeverityBlock,
		Category: domain.CategoryArchitecture,
		File:     "b.go",
		Message:  "block finding",
	}
	result := domain.CheckResult{
		Name:     "architecture:guard",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{block},
		Source:   domain.SourceAgent,
	}

	// Block-severity finding under a WARN override → StatusWarn, never PASS.
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategoryArchitecture: domain.EnforcementBlock,
		},
		SourceRules: map[domain.Source]map[domain.Category]domain.Enforcement{
			domain.SourceAgent: {domain.CategoryArchitecture: domain.EnforcementWarn},
		},
	}
	status, _ := NewEngine(p).Evaluate(result)
	if status != domain.StatusWarn {
		t.Errorf("block finding under warn override: status = %q, want %q", status, domain.StatusWarn)
	}

	// Block-severity finding under a SKIP override → StatusSkip, never PASS.
	p.SourceRules = map[domain.Source]map[domain.Category]domain.Enforcement{
		domain.SourceAgent: {domain.CategoryArchitecture: domain.EnforcementSkip},
	}
	status, _ = NewEngine(p).Evaluate(result)
	if status != domain.StatusSkip {
		t.Errorf("block finding under skip override: status = %q, want %q", status, domain.StatusSkip)
	}
}

func TestG16_SourceOverrideWarnModeCap(t *testing.T) {
	block := domain.Finding{
		RuleID:   "arch:guard",
		Severity: domain.SeverityBlock,
		Category: domain.CategoryArchitecture,
		File:     "b.go",
		Message:  "block finding",
	}
	result := domain.CheckResult{
		Name:     "architecture:guard",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{block},
		Source:   domain.SourceAgent,
	}

	// mode:warn caps a source override of BLOCK at WARN — never PASS, and the
	// override cannot escape the global mode.
	p := Policy{
		Mode: "warn",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategoryArchitecture: domain.EnforcementBlock,
		},
		SourceRules: map[domain.Source]map[domain.Category]domain.Enforcement{
			domain.SourceAgent: {domain.CategoryArchitecture: domain.EnforcementBlock},
		},
	}
	status, _ := NewEngine(p).Evaluate(result)
	if status != domain.StatusWarn {
		t.Errorf("mode:warn + block override: status = %q, want %q", status, domain.StatusWarn)
	}
}

func TestG16_EmptySourceUsesGlobal(t *testing.T) {
	finding := domain.Finding{
		RuleID:   "dup:similar",
		Severity: domain.SeverityWarn,
		Category: domain.CategoryDuplication,
		File:     "a.go",
		Message:  "duplication finding",
	}
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategoryDuplication: domain.EnforcementWarn,
		},
		SourceRules: map[domain.Source]map[domain.Category]domain.Enforcement{
			domain.SourceAgent: {domain.CategoryDuplication: domain.EnforcementSkip},
		},
	}

	// Source empty → no override applies, global warn rule → StatusWarn.
	result := domain.CheckResult{
		Name:     "duplication:check",
		Status:   domain.StatusWarn,
		Findings: []domain.Finding{finding},
	}
	status, _ := NewEngine(p).Evaluate(result)
	if status != domain.StatusWarn {
		t.Errorf("empty source: status = %q, want %q (global rules only)", status, domain.StatusWarn)
	}
}

// --- G20 gate: suppression maturity (P1-2) ---
//
// Engine tests — these must not depend on kern at all.

func TestG20_SuppressionMatches(t *testing.T) {
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategorySecret: domain.EnforcementBlock,
		},
		Suppressions: []Suppression{
			{
				RuleID:   "secret:hardcoded-secret",
				File:     "testdata/*",
				Reason:   "placeholder credentials in test fixtures",
				Reviewer: "platform-eng",
				Expires:  time.Now().Add(24 * time.Hour),
			},
		},
	}
	e := NewEngine(p)

	finding := domain.Finding{
		RuleID:   "secret:hardcoded-secret",
		Severity: domain.SeverityBlock,
		Category: domain.CategorySecret,
		File:     "testdata/creds.yaml",
		Message:  "hardcoded secret",
	}
	result := domain.CheckResult{
		Name:     "secret:scan",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{finding},
	}

	status, findings := e.Evaluate(result)
	// The suppressed block no longer forces BLOCK: findings exist but none
	// block, so the result is WARN (existing "findings exist but none block
	// → WARN" behavior).
	if status != domain.StatusWarn {
		t.Errorf("Evaluate(suppressed block) status = %q, want %q", status, domain.StatusWarn)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if !f.Suppressed {
		t.Error("Suppressed = false, want true")
	}
	if f.SuppressionReason != "placeholder credentials in test fixtures" {
		t.Errorf("SuppressionReason = %q, want the suppression reason", f.SuppressionReason)
	}
	if f.Severity != domain.SeverityInfo {
		t.Errorf("Severity = %q, want %q (downgraded, visible, never blocking)", f.Severity, domain.SeverityInfo)
	}
}

func TestG20_SuppressionRuleOnly(t *testing.T) {
	// Empty File in the suppression => matches any file.
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategorySecret: domain.EnforcementBlock,
		},
		Suppressions: []Suppression{
			{RuleID: "secret:hardcoded-secret", Reason: "fixture creds", Reviewer: "platform-eng", Expires: time.Now().Add(24 * time.Hour)},
		},
	}
	finding := domain.Finding{
		RuleID:   "secret:hardcoded-secret",
		Severity: domain.SeverityBlock,
		Category: domain.CategorySecret,
		File:     "src/deep/nested/creds.go",
	}
	status, findings := NewEngine(p).Evaluate(domain.CheckResult{
		Name:     "secret:scan",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{finding},
	})
	if status != domain.StatusWarn {
		t.Errorf("status = %q, want %q (rule-only suppression matches any file)", status, domain.StatusWarn)
	}
	if !findings[0].Suppressed {
		t.Error("Suppressed = false, want true (empty File matches any file)")
	}
}

func TestG20_ExpiredSuppressionIgnored(t *testing.T) {
	// An expired suppression must not match: the finding stays BLOCK.
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategorySecret: domain.EnforcementBlock,
		},
		Suppressions: []Suppression{
			{RuleID: "secret:hardcoded-secret", Reason: "stale", Reviewer: "platform-eng", Expires: time.Now().Add(-24 * time.Hour)},
		},
	}
	finding := domain.Finding{
		RuleID:   "secret:hardcoded-secret",
		Severity: domain.SeverityBlock,
		Category: domain.CategorySecret,
		File:     "creds.go",
	}
	status, findings := NewEngine(p).Evaluate(domain.CheckResult{
		Name:     "secret:scan",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{finding},
	})
	if status != domain.StatusBlock {
		t.Errorf("status = %q, want %q (expired suppression must not lift the block)", status, domain.StatusBlock)
	}
	if findings[0].Suppressed {
		t.Error("Suppressed = true, want false (expired suppression ignored)")
	}
	if findings[0].Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", findings[0].Severity, domain.SeverityBlock)
	}
}

func TestG20_OwnerAttached(t *testing.T) {
	// Owners map stamps finding.Owner for routing; multiple owners are joined.
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategorySecret: domain.EnforcementBlock,
		},
		Owners: map[string][]string{
			"secret:hardcoded-secret": {"platform-eng", "sec-review"},
		},
	}
	finding := domain.Finding{
		RuleID:   "secret:hardcoded-secret",
		Severity: domain.SeverityBlock,
		Category: domain.CategorySecret,
		File:     "creds.go",
	}
	_, findings := NewEngine(p).Evaluate(domain.CheckResult{
		Name:     "secret:scan",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{finding},
	})
	if findings[0].Owner != "platform-eng, sec-review" {
		t.Errorf("Owner = %q, want %q (multiple owners joined)", findings[0].Owner, "platform-eng, sec-review")
	}
}

func TestG20_SuppressionDoesNotAffectOtherRules(t *testing.T) {
	// A block finding for an un-suppressed rule still BLOCKs.
	p := Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategoryArchitecture: domain.EnforcementBlock,
		},
		Suppressions: []Suppression{
			{RuleID: "secret:hardcoded-secret", Reason: "fixture creds", Reviewer: "platform-eng", Expires: time.Now().Add(24 * time.Hour)},
		},
	}
	finding := domain.Finding{
		RuleID:   "architecture:boundary-violation",
		Severity: domain.SeverityBlock,
		Category: domain.CategoryArchitecture,
		File:     "web/web.go",
	}
	status, findings := NewEngine(p).Evaluate(domain.CheckResult{
		Name:     "architecture:guard",
		Status:   domain.StatusBlock,
		Findings: []domain.Finding{finding},
	})
	if status != domain.StatusBlock {
		t.Errorf("status = %q, want %q (suppression of another rule must not lift this block)", status, domain.StatusBlock)
	}
	if findings[0].Suppressed {
		t.Error("Suppressed = true, want false")
	}
}
