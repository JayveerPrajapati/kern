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
//
// The YAML format is intentionally simple (no external YAML dependency):
//
//	rules:
//	  - id: no-payments-deps
//	    type: MUST_NOT
//	    category: architecture
//	    description: "payments cannot depend on marketing"
//	    cannot_depend_on:
//	      - marketing
//	  - id: never-log-secrets
//	    type: MUST_NOT
//	    category: security
//	    description: "secrets must never be logged"
//	    never_log: true
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
		case "cannot_depend_on", "require_tests":
			// Parse list items on subsequent indented lines.
			list := parseListLines(lines, i+1)
			if key == "cannot_depend_on" {
				current.CannotDependOn = list
			} else {
				current.RequireTests = list
			}
		}
	}
	if current != nil {
		c.Rules = append(c.Rules, *current)
	}
	return c, nil
}

// parseListLines reads subsequent "- value" lines starting from startIndex.
// Stops when it encounters a line that is a new rule entry (contains ":")
// or a non-list, non-empty line.
func parseListLines(lines []string, startIndex int) []string {
	var result []string
	for j := startIndex; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, "- ") {
			// If the item contains ":", it's a new rule entry (e.g. "- id: foo"),
			// not a list item. Stop.
			if strings.Contains(t[2:], ":") {
				break
			}
			result = append(result, strings.TrimSpace(t[2:]))
		} else if t == "" || strings.HasPrefix(t, "#") {
			continue
		} else {
			break // end of list
		}
	}
	return result
}

// ValidatePlan checks a plan against the constitution. Returns a PlanValidation
// with any violations found. Strict Plan Phase 8 P0: Plan → Architecture →
// Security → Constraints → Impact → Risk → Policy.
//
// A MUST or MUST_NOT violation is blocking (PlanValidation.Passed = false).
// SHOULD and SHOULD_NOT violations are warnings (non-blocking).
func ValidatePlan(plan domain.Plan, constitution *domain.Constitution) domain.PlanValidation {
	pv := domain.PlanValidation{Passed: true}

	for _, rule := range constitution.Rules {
		violation := checkRule(plan, rule)
		if violation != nil {
			pv.Violations = append(pv.Violations, *violation)
			if violation.IsBlocking() {
				pv.Passed = false
			}
		}
	}
	return pv
}

// checkRule checks a single constitution rule against the plan. Returns nil
// if the plan complies with the rule.
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
