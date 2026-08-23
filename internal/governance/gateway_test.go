package governance

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
)

// TestTaskBoundaryCheckPath verifies the task boundary allows/denies paths
// correctly.
func TestTaskBoundaryCheckPath(t *testing.T) {
	b := domain.TaskBoundary{
		TaskID:       "t1",
		AllowedPaths: []string{"UserService", "UserRepository", "CacheService", "tests/"},
		DeniedPaths:  []string{"payments/", "production/", "secrets/"},
	}

	cases := []struct {
		path string
		want bool
	}{
		{"UserService", true},
		{"UserService/user.go", true},
		{"UserRepository", true},
		{"tests/user_test.go", true},
		{"payments/refund.go", false},
		{"production/config.yaml", false},
		{"secrets/api_key.txt", false},
		{"other/file.go", false}, // not in allowed paths
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := b.CheckPath(tc.path)
			if got != tc.want {
				t.Errorf("CheckPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestTaskBoundaryEmptyAllowlist verifies that an empty allowlist allows all
// non-denied paths.
func TestTaskBoundaryEmptyAllowlist(t *testing.T) {
	b := domain.TaskBoundary{
		DeniedPaths: []string{"secrets/"},
	}
	if !b.CheckPath("any/file.go") {
		t.Error("empty allowlist should allow non-denied paths")
	}
	if b.CheckPath("secrets/key.txt") {
		t.Error("denied path should be denied even with empty allowlist")
	}
}

// TestSafetyBudgetExceeded verifies the safety budget detects limit exceeded.
func TestSafetyBudgetExceeded(t *testing.T) {
	b := domain.SafetyBudget{
		MaxToolCalls: 3,
		MaxFiles:     2,
		MaxTokens:    100,
	}
	b.Start()

	// Within limits.
	b.TrackToolCall()
	b.TrackToolCall()
	exceeded, _ := b.Exceeded()
	if exceeded {
		t.Fatal("should not be exceeded after 2/3 tool calls")
	}

	// Exceed tool calls.
	b.TrackToolCall()
	b.TrackToolCall() // 4 > 3
	exceeded, reason := b.Exceeded()
	if !exceeded {
		t.Fatal("should be exceeded after 4 tool calls (max 3)")
	}
	if reason == "" {
		t.Fatal("reason should not be empty")
	}
}

// TestToolGatewayBoundaryDeny verifies the gateway denies resources outside
// the task boundary.
func TestToolGatewayBoundaryDeny(t *testing.T) {
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	boundary := domain.TaskBoundary{
		TaskID:       "t1",
		AllowedPaths: []string{"UserService/"},
		DeniedPaths:  []string{"payments/"},
	}

	allowed, _, _, err := gw.Evaluate("agent-1", "t1", "payments/refund.go", "write", boundary, nil)
	if allowed {
		t.Fatal("should deny payments/ path")
	}
	if err == nil {
		t.Fatal("should return error for denied path")
	}
}

// TestToolGatewayBudgetExceeded verifies the gateway denies when budget is
// exceeded.
func TestToolGatewayBudgetExceeded(t *testing.T) {
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	boundary := domain.TaskBoundary{TaskID: "t1"}
	budget := &domain.SafetyBudget{MaxToolCalls: 1}
	budget.Start()
	budget.TrackToolCall()
	budget.TrackToolCall() // exceed

	allowed, _, _, err := gw.Evaluate("agent-1", "t1", "some/file.go", "read", boundary, budget)
	if allowed {
		t.Fatal("should deny when budget exceeded")
	}
	if err == nil {
		t.Fatal("should return error when budget exceeded")
	}
}

// TestDefaultSafetyBudget verifies the default budget has sane limits.
func TestDefaultSafetyBudget(t *testing.T) {
	b := domain.DefaultSafetyBudget()
	if b.MaxFiles <= 0 {
		t.Error("MaxFiles should be > 0")
	}
	if b.MaxToolCalls <= 0 {
		t.Error("MaxToolCalls should be > 0")
	}
	if b.MaxTokens <= 0 {
		t.Error("MaxTokens should be > 0")
	}
	if b.MaxRuntime <= 0 {
		t.Error("MaxRuntime should be > 0")
	}
	if len(b.AllowedEnvs) == 0 {
		t.Error("AllowedEnvs should not be empty")
	}
}

func TestTaskScopeCheckPathAndEnv(t *testing.T) {
	s := domain.TaskScope{
		TaskID:      "t1",
		Paths:       []string{"UserService/"},
		DeniedPaths: []string{"payments/"},
		Envs:        []string{"development", "staging"},
	}
	if !s.CheckPath("UserService/user.go") {
		t.Error("CheckPath should allow UserService path")
	}
	if s.CheckPath("payments/refund.go") {
		t.Error("CheckPath should deny payments path")
	}
	if !s.CheckEnv("development") {
		t.Error("CheckEnv should allow development")
	}
	if s.CheckEnv("production") {
		t.Error("CheckEnv should deny production")
	}
	// Empty Envs allows everything.
	s2 := domain.TaskScope{Envs: nil}
	if !s2.CheckEnv("anything") {
		t.Error("empty env scope should allow anything")
	}
}

func TestEvaluateScopedEnvDeny(t *testing.T) {
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	scope := domain.TaskScope{TaskID: "t1", Envs: []string{"development"}}
	res := gw.EvaluateScoped("agent-1", "t1", "file.go", "read", "production", scope, nil)
	if res.Decision != domain.DecisionDenied {
		t.Errorf("decision = %q, want DENIED", res.Decision)
	}
	if res.Deny == nil || res.Deny.Stage != "env" {
		t.Errorf("deny = %+v, want env stage", res.Deny)
	}
	if res.Deny.Reason == "" {
		t.Error("deny reason should be non-empty (explain-deny P7.6)")
	}
}

func TestEvaluateScopedPathDeny(t *testing.T) {
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	scope := domain.TaskScope{TaskID: "t1", Paths: []string{"ok/"}, DeniedPaths: []string{"bad/"}}
	res := gw.EvaluateScoped("agent-1", "t1", "bad/x.go", "write", "", scope, nil)
	if res.Decision != domain.DecisionDenied || res.Deny.Stage != "boundary" {
		t.Errorf("res = %+v, want DENIED/boundary", res)
	}
}

func TestEvaluateScopedBudgetPause(t *testing.T) {
	fw := firewall.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "a", "coder", []identity.Permission{{Resource: "file.go", Action: "read"}}))
	gw := NewToolGateway(fw)
	scope := domain.TaskScope{TaskID: "t1"}
	budget := &domain.SafetyBudget{MaxToolCalls: 1}
	budget.Start()
	budget.TrackToolCall()
	budget.TrackToolCall() // exceed
	res := gw.EvaluateScoped("agent-1", "t1", "file.go", "read", "", scope, budget)
	if res.Decision != domain.DecisionPaused {
		t.Errorf("decision = %q, want PAUSE", res.Decision)
	}
	if res.Deny == nil || res.Deny.Stage != "budget" {
		t.Errorf("deny = %+v, want budget stage", res.Deny)
	}
}

func TestDryRunDoesNotMutateBudget(t *testing.T) {
	fw := firewall.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "a", "coder", []identity.Permission{{Resource: "file.go", Action: "read"}}))
	gw := NewToolGateway(fw)
	scope := domain.TaskScope{TaskID: "t1"}
	budget := &domain.SafetyBudget{MaxToolCalls: 3}
	budget.Start()
	budget.TrackToolCall()
	before, _ := budget.Exceeded()

	// Dry run for an ALLOW path — must NOT advance the live budget.
	res := gw.DryRun("agent-1", "t1", "file.go", "read", "", scope, budget)
	if res.Decision != domain.DecisionAllowed {
		t.Errorf("dry run decision = %q, want ALLOW", res.Decision)
	}
	after, _ := budget.Exceeded()
	if before != after {
		t.Errorf("dry run mutated live budget: before=%v after=%v", before, after)
	}
}

func TestEvaluateScopedFirewallDeny(t *testing.T) {
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	scope := domain.TaskScope{TaskID: "t1"}
	// An unknown agent is denied by the firewall.
	res := gw.EvaluateScoped("ghost-agent", "t1", "file.go", "read", "", scope, nil)
	if res.Decision != domain.DecisionDenied || res.Deny.Stage != "firewall" {
		t.Errorf("res = %+v, want DENIED/firewall", res)
	}
}
