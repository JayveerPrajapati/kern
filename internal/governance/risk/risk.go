// Package risk provides deterministic risk scoring over domain.Policy rules.
package risk

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// RiskAssessor evaluates the risk of a proposed action against a loaded set of
// policies. It is deterministic: the same resource+action always produces the
// same Risk for a given policy set.
type RiskAssessor struct {
	policies []domain.Policy
}

// NewRiskAssessor creates an assessor with the given policies loaded.
func NewRiskAssessor(policies []domain.Policy) *RiskAssessor {
	return &RiskAssessor{policies: policies}
}

// Policies returns a copy of the policies loaded in this assessor.
func (r *RiskAssessor) Policies() []domain.Policy {
	out := make([]domain.Policy, len(r.policies))
	copy(out, r.policies)
	return out
}

// DefaultPolicies returns the default risk rules. Each rule encodes the risk
// level for a resource+action pair in its Rule body as "<LEVEL>
// <resource>.<action>", e.g. "MEDIUM source.write". HIGH/CRITICAL require
// approval; "CRITICAL database.drop" is always blocked.
func DefaultPolicies() []domain.Policy {
	return []domain.Policy{
		{
			ID:          "pol-source-write",
			Name:        "source_write",
			Description: "Changes to source code.",
			Rule:        "MEDIUM source.write",
			Scope:       "source",
			Enabled:     true,
		},
		{
			ID:          "pol-doc-write",
			Name:        "documentation_write",
			Description: "Changes to documentation.",
			Rule:        "LOW documentation.write",
			Scope:       "documentation",
			Enabled:     true,
		},
		{
			ID:          "pol-security-write",
			Name:        "security_sensitive_write",
			Description: "Changes to security-sensitive files (approval required).",
			Rule:        "HIGH security.write",
			Scope:       "security",
			Enabled:     true,
		},
		{
			ID:          "pol-prod-deploy",
			Name:        "production_deploy",
			Description: "Deploying to production (approval required).",
			Rule:        "CRITICAL production.deploy",
			Scope:       "production",
			Enabled:     true,
		},
		{
			ID:          "pol-db-drop",
			Name:        "database_drop",
			Description: "Dropping a database (always blocked).",
			Rule:        "CRITICAL database.drop",
			Scope:       "database",
			Enabled:     true,
		},
		{
			ID:          "pol-test-write",
			Name:        "test_write",
			Description: "Changes to test files.",
			Rule:        "LOW tests.write",
			Scope:       "tests",
			Enabled:     true,
		},
		{
			ID:          "pol-config-write",
			Name:        "config_write",
			Description: "Changes to configuration.",
			Rule:        "MEDIUM config.write",
			Scope:       "config",
			Enabled:     true,
		},
	}
}

// riskLevel is the numeric rank used for comparisons. Higher means riskier.
type riskLevel int

const (
	riskLow      riskLevel = iota + 1 // LOW
	riskMedium                        // MEDIUM
	riskHigh                          // HIGH
	riskCritical                      // CRITICAL
)

// rank maps a domain.RiskLevel to its numeric rank.
func rank(level domain.RiskLevel) riskLevel {
	switch level {
	case domain.RiskCritical:
		return riskCritical
	case domain.RiskHigh:
		return riskHigh
	case domain.RiskMedium:
		return riskMedium
	default:
		return riskLow
	}
}

// score maps a risk level to a 0.0–1.0 score. This is the deterministic value
// stored in domain.Risk.Score.
func score(level domain.RiskLevel) float64 {
	switch level {
	case domain.RiskCritical:
		return 1.0
	case domain.RiskHigh:
		return 0.75
	case domain.RiskMedium:
		return 0.5
	default:
		return 0.25
	}
}

// policyMatchFor parses a policy Rule of the form "<LEVEL> <resource>.<action>"
// and returns the level when the policy applies to the given resource+action.
func policyMatchFor(p domain.Policy, resource, action string) (domain.RiskLevel, bool) {
	fields := strings.Fields(p.Rule)
	if len(fields) != 2 {
		return "", false
	}
	level := domain.RiskLevel(strings.ToUpper(fields[0]))
	target := strings.SplitN(fields[1], ".", 2)
	if len(target) != 2 {
		return "", false
	}
	if target[0] == resource && target[1] == action {
		return level, true
	}
	return "", false
}

// AssessAction evaluates the risk of an action against the policy set.
// "resource" is what is being acted on; "action" is what is being done. It
// returns a domain.Risk whose Level is the highest matching policy level. If
// no policy matches, the risk defaults to LOW. The result is deterministic.
func (r *RiskAssessor) AssessAction(resource, action string) domain.Risk {
	best := domain.RiskLow
	var bestRank riskLevel = riskLow
	factors := []string{}
	mitigation := ""

	for _, p := range r.policies {
		if !p.Enabled {
			continue
		}
		level, ok := policyMatchFor(p, resource, action)
		if !ok {
			continue
		}
		factors = append(factors, fmt.Sprintf("%s: %s", p.Name, level))
		if rank(level) > bestRank {
			best = level
			bestRank = rank(level)
			mitigation = p.Description
		}
	}

	if best == domain.RiskLow {
		mitigation = "No risk policy matched; defaulting to LOW risk."
		if len(factors) == 0 {
			factors = append(factors, fmt.Sprintf("%s:%s not covered by policy", resource, action))
		}
	}

	return domain.Risk{
		Level:      best,
		Factors:    factors,
		Score:      score(best),
		Mitigation: mitigation,
	}
}
