package governance

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestDefaultPoliciesCoverSpec(t *testing.T) {
	policies := DefaultPolicies()
	expected := map[string]domain.RiskLevel{
		"source.write":        domain.RiskMedium,
		"documentation.write": domain.RiskLow,
		"security.write":      domain.RiskHigh,
		"production.deploy":   domain.RiskCritical,
		"database.drop":       domain.RiskCritical,
		"tests.write":         domain.RiskLow,
		"config.write":        domain.RiskMedium,
	}
	assessor := NewRiskAssessor(policies)
	for resAction, want := range expected {
		parts := splitRA(resAction)
		got := assessor.AssessAction(parts[0], parts[1])
		if got.Level != want {
			t.Errorf("AssessAction(%q) level = %s, want %s", resAction, got.Level, want)
		}
	}
}

func TestAssessActionLevels(t *testing.T) {
	assessor := NewRiskAssessor(DefaultPolicies())
	cases := []struct {
		resource, action string
		want             domain.RiskLevel
		wantScore        float64
	}{
		{"source", "write", domain.RiskMedium, 0.5},
		{"documentation", "write", domain.RiskLow, 0.25},
		{"security", "write", domain.RiskHigh, 0.75},
		{"production", "deploy", domain.RiskCritical, 1.0},
		{"database", "drop", domain.RiskCritical, 1.0},
		{"tests", "write", domain.RiskLow, 0.25},
		{"config", "write", domain.RiskMedium, 0.5},
	}
	for _, c := range cases {
		r := assessor.AssessAction(c.resource, c.action)
		if r.Level != c.want {
			t.Errorf("AssessAction(%q,%q) level = %s, want %s", c.resource, c.action, r.Level, c.want)
		}
		if r.Score != c.wantScore {
			t.Errorf("AssessAction(%q,%q) score = %v, want %v", c.resource, c.action, r.Score, c.wantScore)
		}
		if len(r.Factors) == 0 {
			t.Errorf("AssessAction(%q,%q) should have factors", c.resource, c.action)
		}
	}
}

func TestAssessActionUnmatchedDefaultsLow(t *testing.T) {
	r := NewRiskAssessor(DefaultPolicies()).AssessAction("storage", "purge")
	if r.Level != domain.RiskLow {
		t.Errorf("level = %s, want LOW", r.Level)
	}
	if r.Score != 0.25 {
		t.Errorf("score = %v, want 0.25", r.Score)
	}
}

func TestAssessActionDisabledPolicyIgnored(t *testing.T) {
	policies := []domain.Policy{
		{ID: "p1", Name: "high_rule", Rule: "CRITICAL secret.write", Scope: "secret", Enabled: false},
	}
	r := NewRiskAssessor(policies).AssessAction("secret", "write")
	if r.Level != domain.RiskLow {
		t.Errorf("level = %s, want LOW (disabled policy ignored)", r.Level)
	}
}

func TestAssessActionDeterministic(t *testing.T) {
	assessor := NewRiskAssessor(DefaultPolicies())
	a := assessor.AssessAction("production", "deploy")
	b := assessor.AssessAction("production", "deploy")
	if a.Level != b.Level || a.Score != b.Score {
		t.Error("AssessAction should be deterministic")
	}
	if len(a.Factors) != len(b.Factors) {
		t.Error("Factors should be deterministic")
	}
}

func TestAssessActionCustomPolicies(t *testing.T) {
	assessor := NewRiskAssessor([]domain.Policy{
		{ID: "c1", Name: "custom", Rule: "HIGH custom.act", Scope: "custom", Enabled: true},
	})
	r := assessor.AssessAction("custom", "act")
	if r.Level != domain.RiskHigh {
		t.Errorf("level = %s, want HIGH", r.Level)
	}
}

func splitRA(resAction string) []string {
	for i := 0; i < len(resAction); i++ {
		if resAction[i] == '.' {
			return []string{resAction[:i], resAction[i+1:]}
		}
	}
	return []string{resAction, ""}
}
