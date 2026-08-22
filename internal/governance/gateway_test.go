package governance

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
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
