package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// LoadConstitution reads .kern/constitution.yaml from the given project root.
// Returns an empty constitution (no rules) if the file does not exist — this
// is not an error, it means the project has no constitution constraints.
// The YAML format is intentionally simple (no external YAML dependency):
// rules:
// - id: no-payments-deps
// type: MUST_NOT
// category: architecture
// description: "payments cannot depend on marketing"
// cannot_depend_on:
// - marketing
// - id: never-log-secrets
// type: MUST_NOT
// category: security
// description: "secrets must never be logged"
// never_log: true
func LoadConstitution(root string) (*domain.Constitution, error) {
	path := filepath.Join(root, ".kern", "constitution.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &domain.Constitution{}, nil // no constitution = no constraints
		}
		return nil, fmt.Errorf("constitution: read %s: %w", path, err)
	}
	return parseConstitution(string(b))
}

// parseConstitution parses a simple YAML-like constitution file. This uses a
// minimal line-based parser (no external YAML dependency, consistent with
// kern's stdlib-only default build).
func parseConstitution(text string) (*domain.Constitution, error) {
	c := &domain.Constitution{}
	lines := strings.Split(text, "\n")
	var current *domain.ConstitutionRule

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// New rule entry: only treat "- " as a new rule if the content
		// after it contains a ":" (key-value pair like "id: foo"). List
		// items like "- marketing" (no colon) are NOT new rules.
		if strings.HasPrefix(trimmed, "- ") && strings.Contains(trimmed[2:], ":") {
			if current != nil {
				c.Rules = append(c.Rules, *current)
			}
			current = &domain.ConstitutionRule{}
			trimmed = strings.TrimSpace(trimmed[2:])
		}

		if current == nil {
			continue
		}

		// Parse key: value pairs.
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"`)

		switch key {
		case "id":
			current.ID = val
		case "type":
			current.Type = domain.ConstraintType(val)
		case "category":
			current.Category = val
		case "description":
			current.Description = val
		case "never_log":
			current.NeverLog = val == "true"
		case "required":
			current.Required = val == "true"
		case "approval":
			current.ApprovalRequired = val == "true"
		case "provenance":
			current.Provenance = val
		case "cannot_depend_on", "require_tests":
			// Parse list items on subsequent indented lines.
			list, nextI := parseListLines(lines, i+1)
			if key == "cannot_depend_on" {
				current.CannotDependOn = list
			} else {
				current.RequireTests = list
			}
			if nextI > i+1 {
				i = nextI - 1
			}
		}
	}
	if current != nil {
		c.Rules = append(c.Rules, *current)
	}
	return c, nil
}

// parseListLines reads subsequent "- value" lines starting from startIndex.
// Stops when it encounters a line that is a new rule entry (e.g. "- id: foo")
// or a non-list, non-empty line.
func parseListLines(lines []string, startIndex int) ([]string, int) {
	var result []string
	j := startIndex
	for ; j < len(lines); j++ {
		raw := lines[j]
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "- ") {
			content := strings.TrimSpace(t[2:])
			// If it's a new rule definition like "- id: ...", stop.
			if strings.HasPrefix(content, "id:") {
				break
			}
			indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
			if indent <= 2 && strings.Contains(content, ":") {
				break
			}
			result = append(result, content)
		} else {
			break // end of list
		}
	}
	return result, j
}

// ValidatePlan checks a plan against the constitution. Returns a PlanValidation
// with any violations found. : Plan → Architecture →
// Security → Constraints → Impact → Risk → Policy.
// A MUST or MUST_NOT violation is blocking (PlanValidation.Passed = false).
// SHOULD and SHOULD_NOT violations are warnings (non-blocking).
func ValidatePlan(plan domain.Plan, constitution *domain.Constitution) domain.PlanValidation {
	pv := domain.PlanValidation{Passed: true}

	for _, rule := range constitution.Rules {
		violation := checkRule(plan, rule)
		if violation != nil {
			// P8.4: propagate the rule's provenance (defaulting to manual) so
			// the origin of the constraint is auditable on every violation.
			if violation.Provenance == "" {
				violation.Provenance = ruleProvenance(rule)
			}
			pv.Violations = append(pv.Violations, *violation)
			if violation.IsBlocking() {
				pv.Passed = false
			}
		}
	}
	if len(pv.Violations) > 0 {
		pv.Provenance = pv.Violations[0].Provenance
	}
	return pv
}

// ruleProvenance returns the provenance of a rule, defaulting to "manual-rule"
// when the rule does not declare one. Valid provenance values are "adr",
// "incident", "policy", "team-rule", and "manual-rule".
func ruleProvenance(rule domain.ConstitutionRule) string {
	switch rule.Provenance {
	case "adr", "incident", "policy", "team-rule", "manual-rule":
		return rule.Provenance
	default:
		return "manual-rule"
	}
}

// SuggestRules proposes NEW constitution rules that would prevent the
// violations found in a validation, or harden against common risk patterns in
// the plan (P8.5). Suggestions are NON-ACTIVATING: they never modify the loaded
// constitution — they only advise a human/governance owner what to add. Each
// suggestion is a draft ConstitutionRule plus a rationale.
// Suggestions are deterministic heuristics over the plan and its violations, no
// LLM involved.
type RuleSuggestion struct {
	Rule      domain.ConstitutionRule `json:"rule"`
	Rationale string                  `json:"rationale"`
}

// SuggestRules inspects a validated plan + its validation result and produces
// non-activating rule suggestions. It converts each blocking violation into a
// draft rule that would catch it, then adds a few defensive defaults for common
// plan patterns (destructive DB changes, public-API-without-tests, logging).
func SuggestRules(plan domain.Plan, validation domain.PlanValidation) []RuleSuggestion {
	var out []RuleSuggestion
	seen := map[string]bool{}

	add := func(id, category string, rule domain.ConstitutionRule, reason string) {
		if seen[id] {
			return
		}
		seen[id] = true
		rule.ID = id
		rule.Category = category
		rule.Provenance = "manual-rule" // suggestions are not yet ratified
		out = append(out, RuleSuggestion{Rule: rule, Rationale: reason})
	}

	// 1. Convert each violation into a rule that prevents it.
	for _, v := range validation.Violations {
		switch v.Category {
		case "architecture":
			if strings.Contains(v.Message, "depends on forbidden") {
				// Message form: "plan depends on forbidden <forbidden> (via <dep>)".
				// Extract the forbidden name, which is the token between "forbidden "
				// and " (via ".
				var forbidden string
				marker := "forbidden "
				if i := strings.Index(v.Message, marker); i >= 0 {
					rest := v.Message[i+len(marker):]
					if j := strings.Index(rest, " (via "); j > 0 {
						forbidden = strings.TrimSpace(rest[:j])
					}
				}
				if forbidden == "" {
					continue // cannot infer; skip
				}
				rule := domain.ConstitutionRule{
					Type:           domain.ConstraintMustNot,
					CannotDependOn: []string{forbidden},
					Description:    "prevent dependency on " + forbidden,
				}
				add("suggest-arch-"+forbidden, "architecture", rule, "blocking violation: "+v.Message)
			}
		case "security":
			rule := domain.ConstitutionRule{
				Type:        domain.ConstraintMustNot,
				NeverLog:    true,
				Description: "secrets must never be logged",
			}
			add("suggest-sec-no-log", "security", rule, "security violation: "+v.Message)
		case "database":
			rule := domain.ConstitutionRule{
				Type:             domain.ConstraintMust,
				ApprovalRequired: true,
				Description:      "destructive database changes require approval",
			}
			add("suggest-db-approval", "database", rule, "database violation: "+v.Message)
		case "testing":
			rule := domain.ConstitutionRule{
				Type:         domain.ConstraintMust,
				RequireTests: []string{"integration"},
				Description:  "public API changes require integration tests",
			}
			add("suggest-test-api", "testing", rule, "testing violation: "+v.Message)
		}
	}

	// 2. Defensive defaults: suggest hardening rules the plan would benefit
	// from, even when no violation fired.
	for _, step := range plan.ImplementationSteps {
		low := strings.ToLower(step)
		if (strings.Contains(low, "log") || strings.Contains(low, "print")) &&
			!seen["suggest-sec-no-log"] {
			add("suggest-sec-no-log", "security", domain.ConstitutionRule{
				Type:        domain.ConstraintMustNot,
				NeverLog:    true,
				Description: "secrets must never be logged",
			}, "plan touches logging; hardening against secret leakage")
		}
		if (strings.Contains(low, "migration") || strings.Contains(low, "drop table")) &&
			!seen["suggest-db-approval"] {
			add("suggest-db-approval", "database", domain.ConstitutionRule{
				Type:             domain.ConstraintMust,
				ApprovalRequired: true,
				Description:      "destructive database changes require approval",
			}, "plan includes destructive DB step; requires approval")
		}
	}
	return out
}
func checkRule(plan domain.Plan, rule domain.ConstitutionRule) *domain.PlanViolation {
	switch rule.Category {
	case "architecture":
		// Check cannot_depend_on: no dependency may match a forbidden name.
		for _, forbidden := range rule.CannotDependOn {
			for _, dep := range plan.Dependencies {
				if strings.Contains(strings.ToLower(dep), strings.ToLower(forbidden)) {
					return &domain.PlanViolation{
						RuleID:   rule.ID,
						Type:     rule.Type,
						Category: rule.Category,
						Message:  fmt.Sprintf("plan depends on forbidden %s (via %s)", forbidden, dep),
					}
				}
			}
		}
	case "security":
		if rule.NeverLog && rule.Type == domain.ConstraintMustNot {
			// Check if the plan involves logging changes.
			for _, step := range plan.ImplementationSteps {
				if strings.Contains(strings.ToLower(step), "log") {
					return &domain.PlanViolation{
						RuleID:   rule.ID,
						Type:     rule.Type,
						Category: rule.Category,
						Message:  "plan involves logging but constitution forbids logging secrets",
					}
				}
			}
		}
	case "database":
		if rule.ApprovalRequired && rule.Type == domain.ConstraintMust {
			for _, step := range plan.ImplementationSteps {
				if strings.Contains(strings.ToLower(step), "migration") ||
					strings.Contains(strings.ToLower(step), "drop table") ||
					strings.Contains(strings.ToLower(step), "destructive") {
					return &domain.PlanViolation{
						RuleID:   rule.ID,
						Type:     rule.Type,
						Category: rule.Category,
						Message:  "plan involves destructive database change requiring approval",
					}
				}
			}
		}
	case "testing":
		if len(rule.RequireTests) > 0 && rule.Type == domain.ConstraintMust {
			// Check if the plan involves public API changes.
			for _, comp := range plan.AffectedComponents {
				if strings.Contains(strings.ToLower(comp), "api") ||
					strings.Contains(strings.ToLower(comp), "handler") {
					// Check if tests are in the plan.
					hasTests := false
					for _, step := range plan.ImplementationSteps {
						if strings.Contains(strings.ToLower(step), "test") {
							hasTests = true
							break
						}
					}
					if !hasTests {
						return &domain.PlanViolation{
							RuleID:   rule.ID,
							Type:     rule.Type,
							Category: rule.Category,
							Message:  "plan changes public API but does not include required tests",
						}
					}
				}
			}
		}
	}
	return nil
}
