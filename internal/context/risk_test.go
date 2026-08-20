package context

import (
	"fmt"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
)

// riskIndex builds a tiny in-memory graph with two roots: a benign source
// symbol (main.go) and a security-sensitive symbol (auth/login.go).
func riskIndex() *index.Index {
	return &index.Index{
		Root: "/risk",
		Symbols: []index.Symbol{
			sym("func", "Helper", "main.go", 1),
			sym("func", "Login", "auth/login.go", 1),
		},
		Calls:     map[string][]string{},
		Callers:   map[string][]string{},
		Pkgs:      map[string]*index.Pkg{},
		UpdatedAt: time.Now(),
	}
}

// riskEngine builds an engine over riskIndex with the given permissions for
// the context-engine agent.
func riskEngine(t *testing.T, perms []governance.Permission) *Engine {
	t.Helper()
	g := intelligence.FromIndex(riskIndex())
	fw := governance.NewFirewall().WithAgents(
		governance.NewAgent(engineAgent, "Context Engine", "analyzer", perms),
	)
	return NewEngine("/risk", &g, nil, fw)
}

// TestAssessRiskSecuritySensitiveHigherThanBenign verifies the risk the engine
// emits reflects the REAL change scope: a change touching a security-sensitive
// file is scored higher and flagged ApprovalRequired/Blocked by the firewall,
// while a benign source change stays an allowed MEDIUM.
func TestAssessRiskSecuritySensitiveHigherThanBenign(t *testing.T) {
	// Agent may write source AND security; security writes are HIGH and
	// require human approval.
	e := riskEngine(t, []governance.Permission{
		{Resource: "source", Action: "write"},
		{Resource: "security", Action: "write"},
	})

	benign, err := e.AnalyzeChange("Helper")
	if err != nil {
		t.Fatalf("AnalyzeChange(Helper): %v", err)
	}
	if len(benign.Risks) != 1 {
		t.Fatalf("expected 1 risk for benign change, got %d", len(benign.Risks))
	}
	br := benign.Risks[0]
	if br.Blocked {
		t.Errorf("benign source change should not be blocked: %+v", br)
	}
	if br.ApprovalRequired {
		t.Errorf("benign source change should not require approval: %+v", br)
	}

	security, err := e.AnalyzeChange("Login")
	if err != nil {
		t.Fatalf("AnalyzeChange(Login): %v", err)
	}
	if len(security.Risks) != 1 {
		t.Fatalf("expected 1 risk for security change, got %d", len(security.Risks))
	}
	sr := security.Risks[0]
	if !sr.ApprovalRequired {
		t.Errorf("security change must require approval (HIGH), got %+v", sr)
	}
	if !sr.Blocked {
		t.Errorf("security change must be blocked pending approval, got %+v", sr)
	}
	if sr.Level == domain.RiskLow || sr.Level == domain.RiskMedium {
		t.Errorf("security change must be scored HIGH/CRITICAL, got %q", sr.Level)
	}
	if riskScore(sr.Level) <= riskScore(br.Level) {
		t.Errorf("security risk (%s) must score strictly above benign (%s)",
			sr.Level, br.Level)
	}
}

// TestAssessRiskDeniedWhenNoPermission verifies a denied firewall decision is
// surfaced as Blocked rather than being discarded as an ordinary Risk.
func TestRiskEngineDeniedWhenNoPermission(t *testing.T) {
	// Agent only has source:write — a security:write change is denied.
	e := riskEngine(t, []governance.Permission{{Resource: "source", Action: "write"}})

	pkt, err := e.AnalyzeChange("Login")
	if err != nil {
		t.Fatalf("AnalyzeChange(Login): %v", err)
	}
	if len(pkt.Risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(pkt.Risks))
	}
	r := pkt.Risks[0]
	if !r.Blocked {
		t.Errorf("denied security change must be flagged Blocked, got %+v", r)
	}
}

// TestRiskEngineDefaultWithoutFirewall verifies a documented default risk is
// returned (not silently dropped) when the engine has no firewall wired.
func TestRiskEngineDefaultWithoutFirewall(t *testing.T) {
	g := intelligence.FromIndex(riskIndex())
	e := NewEngine("/risk", &g, nil, nil) // no firewall

	pkt, err := e.AnalyzeChange("Helper")
	if err != nil {
		t.Fatalf("AnalyzeChange(Helper): %v", err)
	}
	if len(pkt.Risks) != 1 {
		t.Fatalf("expected a documented default risk, got %d", len(pkt.Risks))
	}
	if pkt.Risks[0].Level != domain.RiskMedium {
		t.Errorf("default risk level = %q, want MEDIUM", pkt.Risks[0].Level)
	}
	if pkt.Risks[0].Blocked || pkt.Risks[0].ApprovalRequired {
		t.Errorf("default risk must not be blocked or require approval: %+v", pkt.Risks[0])
	}
}

// TestAssessRiskBlastRadiusCapsSourceRisk verifies risk is proportional to the
// blast radius: a single-root source change must never be CRITICAL (capped at
// MEDIUM for an isolated change), while a large change may keep CRITICAL.
func TestAssessRiskBlastRadiusCapsSource(t *testing.T) {
	// Simulate a firewall whose default source:write policy is CRITICAL (the
	// pathological default that motivated B8) so we can observe the cap.
	e := riskEngine(t, []governance.Permission{{Resource: "source", Action: "write"}})
	e.firewall = e.firewall.WithPolicies([]domain.Policy{
		{ID: "p", Name: "source_write", Description: "source", Rule: "CRITICAL source.write", Scope: "source", Enabled: true},
	})

	// 1-root change: must be capped to MEDIUM, never CRITICAL.
	pkt1, err := e.AnalyzeChange("Helper")
	if err != nil {
		t.Fatalf("AnalyzeChange(Helper): %v", err)
	}
	if len(pkt1.Risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(pkt1.Risks))
	}
	r1 := pkt1.Risks[0]
	if r1.Level == domain.RiskCritical || r1.Level == domain.RiskHigh {
		t.Errorf("1-root source change should be capped at MEDIUM, got %q", r1.Level)
	}
	if r1.Level != domain.RiskMedium {
		t.Errorf("1-root source change level = %q, want MEDIUM", r1.Level)
	}

	// Large change (many roots) keeps CRITICAL from the firewall.
	roots := make([]domain.Symbol, 0, 10)
	for i := 0; i < 10; i++ {
		roots = append(roots, domain.Symbol{Name: fmt.Sprintf("Sym%d", i), File: "main.go", Kind: "func"})
	}
	pktBig := e.assemble("large change", "main.go", roots)
	if len(pktBig.Risks) != 1 {
		t.Fatalf("expected 1 risk for large change, got %d", len(pktBig.Risks))
	}
	rb := pktBig.Risks[0]
	if rb.Level != domain.RiskCritical {
		t.Errorf("10-root source change should keep CRITICAL, got %q", rb.Level)
	}
}

// TestAssessRiskSecurityNotDowngraded verifies a security-sensitive change
// stays HIGH regardless of blast radius — security escalation is resource-based,
// not scope-based.
func TestAssessRiskSecurityNotDowngraded(t *testing.T) {
	e := riskEngine(t, []governance.Permission{
		{Resource: "source", Action: "write"},
		{Resource: "security", Action: "write"},
	})
	pkt, err := e.AnalyzeChange("Login")
	if err != nil {
		t.Fatalf("AnalyzeChange(Login): %v", err)
	}
	if len(pkt.Risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(pkt.Risks))
	}
	if pkt.Risks[0].Level != domain.RiskHigh {
		t.Errorf("1-root security change must stay HIGH, got %q", pkt.Risks[0].Level)
	}
}
