package domain

import "testing"

// TestRiskFromImpactTierTable pins the shared classifier's documented tier
// table (D3): every row of RiskFromImpact's contract — isolated/small/wide,
// services, cycles, and the security floor — maps to exactly one tier and
// factor.
func TestRiskFromImpactTierTable(t *testing.T) {
	cases := []struct {
		name       string
		imp        ImpactRisk
		want       RiskLevel
		wantFactor string
	}{
		{"isolated", ImpactRisk{}, RiskLow, "blast-radius:isolated"},
		{"small", ImpactRisk{AffectedCount: 1}, RiskMedium, "blast-radius:moderate"},
		{"moderate", ImpactRisk{AffectedCount: 10}, RiskMedium, "blast-radius:moderate"},
		{"wide", ImpactRisk{AffectedCount: 11}, RiskHigh, "blast-radius:large"},
		{"service", ImpactRisk{ServicesAffected: 1}, RiskHigh, "blast-radius:large"},
		{"service_and_small", ImpactRisk{AffectedCount: 2, ServicesAffected: 1}, RiskHigh, "blast-radius:large"},
		{"cycle", ImpactRisk{CreatesCycle: true}, RiskHigh, "risk:cycle"},
		{"cycle_beats_isolated", ImpactRisk{CreatesCycle: true}, RiskHigh, "risk:cycle"},
		{"security_isolated", ImpactRisk{SecuritySensitive: true}, RiskHigh, "blast-radius:isolated"},
		{"security_wide", ImpactRisk{AffectedCount: 20, SecuritySensitive: true}, RiskHigh, "blast-radius:large"},
		{"security_critical_severity", ImpactRisk{SecuritySensitive: true, Severity: RiskCritical}, RiskCritical, "blast-radius:isolated"},
		{"negative_affected", ImpactRisk{AffectedCount: -3}, RiskLow, "blast-radius:isolated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, factor := RiskFromImpact(tc.imp)
			if got != tc.want {
				t.Errorf("RiskFromImpact(%+v) tier = %q, want %q", tc.imp, got, tc.want)
			}
			if factor != tc.wantFactor {
				t.Errorf("RiskFromImpact(%+v) factor = %q, want %q", tc.imp, factor, tc.wantFactor)
			}
		})
	}
}

// TestRiskFromImpactAgreementLocksTheDivergenceFix pins D3 directly: the tier
// the what-if simulation derives from a change's transitive impact and the
// tier the context engine derives from the same impact are ONE function, so a
// rename that affects 260+ symbols is HIGH under both — never "isolated".
func TestRiskFromImpactAgreementLocksTheDivergenceFix(t *testing.T) {
	// The D3 repro shape: renaming NewServer transitively affects 263 symbols.
	wide := ImpactRisk{AffectedCount: 263}
	tier, factor := RiskFromImpact(wide)
	if tier != RiskHigh {
		t.Errorf("263-affected change tier = %q, want HIGH", tier)
	}
	if factor != "blast-radius:large" {
		t.Errorf("263-affected change factor = %q, want blast-radius:large", factor)
	}
}

// TestRiskFromImpactApprovalThreshold pins the human-approval contract: HIGH
// is the threshold, and only the wide/service/cycle/security rows reach it —
// an isolated or small change never requires approval.
func TestRiskFromImpactApprovalThreshold(t *testing.T) {
	requiresApproval := func(imp ImpactRisk) bool {
		tier, _ := RiskFromImpact(imp)
		return tier == RiskHigh || tier == RiskCritical
	}
	if requiresApproval(ImpactRisk{}) {
		t.Error("isolated change must not cross the approval threshold")
	}
	if requiresApproval(ImpactRisk{AffectedCount: 10}) {
		t.Error("10-affected change must not cross the approval threshold")
	}
	if !requiresApproval(ImpactRisk{AffectedCount: 11}) {
		t.Error("11-affected change must cross the approval threshold (HIGH)")
	}
	if !requiresApproval(ImpactRisk{SecuritySensitive: true}) {
		t.Error("security-sensitive change must cross the approval threshold (HIGH)")
	}
}
