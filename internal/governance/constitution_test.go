package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestLoadConstitutionMissingFile verifies a missing constitution file returns
// an empty constitution (not an error).
func TestLoadConstitutionMissingFile(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConstitution(dir)
	if err != nil {
		t.Fatalf("LoadConstitution: %v", err)
	}
	if len(c.Rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(c.Rules))
	}
}

// TestLoadConstitution verifies a constitution file is parsed correctly.
func TestLoadConstitution(t *testing.T) {
	dir := t.TempDir()
	kernDir := filepath.Join(dir, ".kern")
	os.MkdirAll(kernDir, 0o755)
	yaml := `rules:
  - id: no-payments-deps
    type: MUST_NOT
    category: architecture
    description: "payments cannot depend on marketing"
    cannot_depend_on:
      - marketing
  - id: never-log-secrets
    type: MUST_NOT
    category: security
    description: "secrets must never be logged"
    never_log: true
  - id: destructive-db-approval
    type: MUST
    category: database
    description: "destructive db changes require approval"
    approval: true
  - id: api-needs-tests
    type: MUST
    category: testing
    description: "public API changes require integration tests"
    require_tests:
      - integration_test
`
	os.WriteFile(filepath.Join(kernDir, "constitution.yaml"), []byte(yaml), 0o644)

	c, err := LoadConstitution(dir)
	if err != nil {
		t.Fatalf("LoadConstitution: %v", err)
	}
	if len(c.Rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(c.Rules))
	}
	// Verify first rule.
	r0 := c.Rules[0]
	if r0.ID != "no-payments-deps" {
		t.Errorf("r0.ID=%q", r0.ID)
	}
	if r0.Type != domain.ConstraintMustNot {
		t.Errorf("r0.Type=%q, want MUST_NOT", r0.Type)
	}
	if len(r0.CannotDependOn) != 1 || r0.CannotDependOn[0] != "marketing" {
		t.Errorf("r0.CannotDependOn=%v, want [marketing]", r0.CannotDependOn)
	}
	// Verify security rule.
	r1 := c.Rules[1]
	if !r1.NeverLog {
		t.Error("r1.NeverLog should be true")
	}
	// Verify database rule.
	r2 := c.Rules[2]
	if !r2.ApprovalRequired {
		t.Error("r2.ApprovalRequired should be true")
	}
	// Verify testing rule.
	r3 := c.Rules[3]
	if len(r3.RequireTests) != 1 || r3.RequireTests[0] != "integration_test" {
		t.Errorf("r3.RequireTests=%v, want [integration_test]", r3.RequireTests)
	}
}

// TestValidatePlanArchitectureViolation verifies a plan violating an
// architecture MUST_NOT is blocking.
func TestValidatePlanArchitectureViolation(t *testing.T) {
	c := &domain.Constitution{
		Rules: []domain.ConstitutionRule{
			{
				ID: "no-payments-deps", Type: domain.ConstraintMustNot, Category: "architecture",
				CannotDependOn: []string{"marketing"},
			},
		},
	}
	plan := domain.Plan{
		AffectedComponents: []string{"payments/checkout"},
		Dependencies:       []string{"marketing/promo"},
	}
	pv := ValidatePlan(plan, c)
	if pv.Passed {
		t.Fatal("plan should not pass (MUST_NOT violation)")
	}
	if len(pv.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(pv.Violations))
	}
	if !pv.Violations[0].IsBlocking() {
		t.Error("violation should be blocking (MUST_NOT)")
	}
}

// TestValidatePlanPasses verifies a plan with no violations passes.
func TestValidatePlanPasses(t *testing.T) {
	c := &domain.Constitution{
		Rules: []domain.ConstitutionRule{
			{
				ID: "no-payments-deps", Type: domain.ConstraintMustNot, Category: "architecture",
				CannotDependOn: []string{"marketing"},
			},
		},
	}
	plan := domain.Plan{
		AffectedComponents: []string{"payments/checkout"},
		Dependencies:       []string{"payments/db"},
	}
	pv := ValidatePlan(plan, c)
	if !pv.Passed {
		t.Fatal("plan should pass (no violations)")
	}
	if len(pv.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(pv.Violations))
	}
}

// TestValidatePlanDatabaseApproval verifies destructive DB changes are
// flagged.
func TestValidatePlanDatabaseApproval(t *testing.T) {
	c := &domain.Constitution{
		Rules: []domain.ConstitutionRule{
			{
				ID: "db-approval", Type: domain.ConstraintMust, Category: "database",
				ApprovalRequired: true,
			},
		},
	}
	plan := domain.Plan{
		ImplementationSteps: []string{"add migration to drop table users"},
	}
	pv := ValidatePlan(plan, c)
	if pv.Passed {
		t.Fatal("plan should not pass (destructive DB change without approval)")
	}
}

// TestValidatePlanTestingRequires verifies API changes without tests are
// flagged.
func TestValidatePlanTestingRequires(t *testing.T) {
	c := &domain.Constitution{
		Rules: []domain.ConstitutionRule{
			{
				ID: "api-tests", Type: domain.ConstraintMust, Category: "testing",
				RequireTests: []string{"integration_test"},
			},
		},
	}
	plan := domain.Plan{
		AffectedComponents: []string{"api/handler"},
		ImplementationSteps: []string{"add new endpoint"},
	}
	pv := ValidatePlan(plan, c)
	if pv.Passed {
		t.Fatal("plan should not pass (API change without tests)")
	}

	// Same plan but WITH tests should pass.
	plan.ImplementationSteps = append(plan.ImplementationSteps, "add integration test")
	pv = ValidatePlan(plan, c)
	if !pv.Passed {
		t.Fatal("plan should pass (API change WITH tests)")
	}
}

func TestValidatePlanPropagatesProvenance(t *testing.T) {
	c := &domain.Constitution{Rules: []domain.ConstitutionRule{
		{ID: "no-payments", Type: domain.ConstraintMustNot, Category: "architecture",
			CannotDependOn: []string{"marketing"}, Provenance: "adr"},
	}}
	plan := domain.Plan{
		Dependencies: []string{"marketing/campaign.go"},
	}
	pv := ValidatePlan(plan, c)
	if pv.Passed {
		t.Fatal("plan should fail (MUST_NOT violated)")
	}
	if len(pv.Violations) != 1 {
		t.Fatalf("violations = %d, want 1", len(pv.Violations))
	}
	// P8.4: provenance propagated from the rule.
	if pv.Violations[0].Provenance != "adr" {
		t.Errorf("violation provenance = %q, want adr", pv.Violations[0].Provenance)
	}
	if pv.Provenance != "adr" {
		t.Errorf("validation provenance = %q, want adr", pv.Provenance)
	}
}

func TestValidatePlanDefaultsProvenance(t *testing.T) {
	c := &domain.Constitution{Rules: []domain.ConstitutionRule{
		{ID: "r1", Type: domain.ConstraintMustNot, Category: "security", NeverLog: true},
	}}
	plan := domain.Plan{ImplementationSteps: []string{"add logging to auth"}}
	pv := ValidatePlan(plan, c)
	if len(pv.Violations) != 1 {
		t.Fatalf("violations = %d, want 1", len(pv.Violations))
	}
	// No declared provenance -> default "manual-rule".
	if pv.Violations[0].Provenance != "manual-rule" {
		t.Errorf("provenance = %q, want manual-rule", pv.Violations[0].Provenance)
	}
}

func TestSuggestRulesFromViolations(t *testing.T) {
	c := &domain.Constitution{Rules: []domain.ConstitutionRule{
		{ID: "no-payments", Type: domain.ConstraintMustNot, Category: "architecture", CannotDependOn: []string{"marketing"}},
	}}
	plan := domain.Plan{Dependencies: []string{"marketing/x.go"}}
	pv := ValidatePlan(plan, c)
	sugg := SuggestRules(plan, pv)
	if len(sugg) == 0 {
		t.Fatal("expected suggestions from architecture violation")
	}
	found := false
	for _, s := range sugg {
		if s.Rule.Category == "architecture" && s.Rule.CannotDependOn[0] == "marketing" {
			found = true
		}
	}
	if !found {
		t.Errorf("architecture suggestion not found: %+v", sugg)
	}
	// Suggestions must be non-activating: they never appear in the constitution.
	if len(c.Rules) != 1 {
		t.Errorf("SuggestRules mutated constitution: rules=%d", len(c.Rules))
	}
}

func TestSuggestRulesDefensive(t *testing.T) {
	plan := domain.Plan{ImplementationSteps: []string{"run migration to drop table users"}}
	pv := domain.PlanValidation{Passed: true}
	suggests := SuggestRules(plan, pv)
	foundDB := false
	for _, s := range suggests {
		if s.Rule.Category == "database" && s.Rule.ApprovalRequired {
			foundDB = true
		}
	}
	if !foundDB {
		t.Errorf("expected database approval suggestion, got %+v", suggests)
	}
}
