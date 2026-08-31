package domain

// ConstraintType is the severity of a constitution constraint.
type ConstraintType string

const (
	ConstraintMust      ConstraintType = "MUST"
	ConstraintMustNot   ConstraintType = "MUST_NOT"
	ConstraintShould    ConstraintType = "SHOULD"
	ConstraintShouldNot ConstraintType = "SHOULD_NOT"
)

// ConstitutionRule is a single engineering rule from .kern/constitution.yaml.
type ConstitutionRule struct {
	ID          string         `json:"id" yaml:"id"`
	Type        ConstraintType `json:"type" yaml:"type"`
	Category    string         `json:"category" yaml:"category"` // architecture, security, data, database, testing
	Description string         `json:"description" yaml:"description"`
	// Architecture rules: package A cannot depend on package B.
	CannotDependOn []string `json:"cannot_depend_on,omitempty" yaml:"cannot_depend_on,omitempty"`
	// Security rules: secrets must never be logged.
	NeverLog bool `json:"never_log,omitempty" yaml:"never_log,omitempty"`
	// Data rules: tenant isolation required.
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
	// Database rules: destructive changes require approval.
	ApprovalRequired bool `json:"approval,omitempty" yaml:"approval,omitempty"`
	// Testing rules: public API changes require integration tests.
	RequireTests []string `json:"require_tests,omitempty" yaml:"require_tests,omitempty"`
	// Provenance records where this rule came from (P8.4): "adr", "incident",
	// "policy", "team-rule", or "manual-rule". Empty means the source is
	// unknown/manual. It is propagated onto violations so the origin of a
	// constraint is auditable.
	Provenance string `json:"provenance,omitempty" yaml:"provenance,omitempty"`
}

// Constitution is the loaded engineering constitution.
type Constitution struct {
	Rules []ConstitutionRule `json:"rules" yaml:"rules"`
}

// PlanValidation is the result of validating a plan against the constitution.
type PlanValidation struct {
	Passed     bool            `json:"passed"`
	Violations []PlanViolation `json:"violations,omitempty"`
	// Provenance records the source of the validation (P8.4), propagated from
	// the constitution rules that produced the violations. It helps auditors
	// trace a decision back to the ADR/incident/policy that required it.
	Provenance string `json:"provenance,omitempty"`
}

// PlanViolation is a single constraint violation found during plan validation.
type PlanViolation struct {
	RuleID     string         `json:"rule_id"`
	Type       ConstraintType `json:"type"`
	Category   string         `json:"category"`
	Message    string         `json:"message"`
	Provenance string         `json:"provenance,omitempty"` // ADR, incident, policy, team-rule, manual-rule
}

// IsBlocking reports whether a violation is a MUST or MUST_NOT (blocking).
func (v PlanViolation) IsBlocking() bool {
	return v.Type == ConstraintMust || v.Type == ConstraintMustNot
}
