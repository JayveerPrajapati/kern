package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestCompileIntentClassification verifies the intent compiler classifies
// common intent patterns into the correct IntentType.
func TestCompileIntentClassification(t *testing.T) {
	cases := []struct {
		intent string
		want   domain.IntentType
	}{
		{"explain how UserService works", domain.IntentUnderstand},
		{"add caching to UserService", domain.IntentCodeChange},
		{"fix the N+1 query in UserRepository", domain.IntentCodeChange},
		{"what if we remove Redis", domain.IntentWhatIf},
		{"simulate splitting PaymentService", domain.IntentWhatIf},
		{"production is failing with 500 errors", domain.IntentIncident},
		{"modernize the monolith", domain.IntentModernization},
		{"check for security vulnerabilities", domain.IntentSecurity},
		{"review this PR", domain.IntentReview},
		{"deploy version 2.0", domain.IntentDeploy},
		{"audit what the AI changed", domain.IntentAudit},
	}
	for _, tc := range cases {
		t.Run(tc.intent, func(t *testing.T) {
			got := CompileIntent(tc.intent)
			if got.Type != tc.want {
				t.Errorf("CompileIntent(%q).Type = %s, want %s", tc.intent, got.Type, tc.want)
			}
		})
	}
}

// TestCompileIntentFields verifies the compiled intent has the expected fields.
func TestCompileIntentFields(t *testing.T) {
	ci := CompileIntent("add caching to UserService")
	if ci.Target != "UserService" {
		t.Errorf("Target=%q, want UserService", ci.Target)
	}
	if ci.Scope != "repository" {
		t.Errorf("Scope=%q, want repository", ci.Scope)
	}
	if ci.Environment != "development" {
		t.Errorf("Environment=%q, want development", ci.Environment)
	}
	if ci.RawText != "add caching to UserService" {
		t.Errorf("RawText=%q", ci.RawText)
	}
}

// TestCompileIntentEnvironmentDerived verifies the compiled environment is
// derived from the intent type: production operations (DEPLOY, INCIDENT) are
// scoped to "production", ordinary development intents default to
// "development".
func TestCompileIntentEnvironmentDerived(t *testing.T) {
	cases := []struct {
		intent string
		want   string
	}{
		{"add caching to UserService", "development"},
		{"deploy version 2.0 to production", "production"},
		{"production is failing with 500 errors", "production"},
	}
	for _, tc := range cases {
		if got := CompileIntent(tc.intent).Environment; got != tc.want {
			t.Errorf("CompileIntent(%q).Environment = %q, want %q", tc.intent, got, tc.want)
		}
	}
}

// TestCompileIntentProductionNotUnconditionalIncident verifies that mentioning
// "production" in a routine change does not force an INCIDENT classification.
// "production" is not itself an incident signal — a code change to production
// code is a CODE_CHANGE; a failing/down/outage statement is an INCIDENT.
func TestCompileIntentProductionNotUnconditionalIncident(t *testing.T) {
	if got := CompileIntent("refactor production code for readability").Type; got != domain.IntentCodeChange {
		t.Errorf("refactor production code: Type=%s, want CODE_CHANGE", got)
	}
	// A real incident still classifies as INCIDENT via its own signal words.
	if got := CompileIntent("production is down").Type; got != domain.IntentIncident {
		t.Errorf("production is down: Type=%s, want INCIDENT", got)
	}
}

// TestSelectWorkflow verifies the workflow selector maps intent types to
// workflows A-E.
func TestSelectWorkflow(t *testing.T) {
	cases := []struct {
		it   domain.IntentType
		want domain.WorkflowID
	}{
		{domain.IntentUnderstand, domain.WorkflowUnderstand},
		{domain.IntentCodeChange, domain.WorkflowSafeChange},
		{domain.IntentWhatIf, domain.WorkflowPredict},
		{domain.IntentIncident, domain.WorkflowOperate},
		{domain.IntentAudit, domain.WorkflowGovern},
	}
	for _, tc := range cases {
		got := SelectWorkflow(tc.it)
		if got != tc.want {
			t.Errorf("SelectWorkflow(%s) = %s, want %s", tc.it, got, tc.want)
		}
	}
}

// TestDefaultCapabilities verifies capabilities are selected for code-change.
func TestDefaultCapabilities(t *testing.T) {
	caps := DefaultCapabilities(domain.IntentCodeChange)
	if len(caps) < 4 {
		t.Fatalf("len(caps)=%d, want >= 4", len(caps))
	}
	// Code-change should include analyze, plan, execute, verify at minimum.
	names := map[string]bool{}
	for _, c := range caps {
		names[c.Name] = true
	}
	for _, required := range []string{"analyze", "plan", "execute", "verify"} {
		if !names[required] {
			t.Errorf("missing capability: %s", required)
		}
	}
}

// TestCapabilitiesToTools verifies tools are flattened from capabilities.
func TestCapabilitiesToTools(t *testing.T) {
	caps := DefaultCapabilities(domain.IntentCodeChange)
	tools := CapabilitiesToTools(caps)
	if len(tools) == 0 {
		t.Fatal("no tools returned")
	}
	// Verify no duplicates.
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool] {
			t.Errorf("duplicate tool: %s", tool)
		}
		seen[tool] = true
	}
}

// TestDefaultCapabilitiesSingleSource verifies DefaultCapabilities resolves
// through the canonical CapabilityRegistry (allCapabilities) rather than a
// parallel ad-hoc list. Every returned capability must carry its Purpose — if
// the two sources ever drift, a capability would come back Purpose-less, which
// flags the duplicate-system regression the gate-4 fix removed.
func TestDefaultCapabilitiesSingleSource(t *testing.T) {
	reg := NewCapabilityRegistry()
	for _, it := range []domain.IntentType{
		domain.IntentUnderstand, domain.IntentCodeChange, domain.IntentWhatIf,
		domain.IntentIncident, domain.IntentModernization, domain.IntentSecurity,
		domain.IntentTest, domain.IntentDeploy, domain.IntentAudit,
	} {
		for _, c := range DefaultCapabilities(it) {
			if c.Purpose == "" {
				t.Errorf("%s: capability %q has no Purpose; registry lookup failed or catalog entry lacks it", it, c.Name)
			}
			// It must be the SAME metadata as the registry holds (single source).
			if want, ok := reg.Get(c.Name); !ok || want.Tools[0] != c.Tools[0] {
				t.Errorf("%s: capability %q does not match the canonical registry entry", it, c.Name)
			}
		}
	}
}

// TestCapabilitiesToAgentsIntentAware verifies the agent team is derived from a
// deterministic capability → role mapping, not from capability Dependencies
// (which are other capabilities, not agents).
func TestCapabilitiesToAgentsIntentAware(t *testing.T) {
	cases := []struct {
		intent string
		want   []string
	}{
		{"add caching", []string{"architect", "planner", "coder", "tester"}},
		{"deploy", []string{"planner", "sre"}},
		{"what if", []string{"architect"}},
		{"audit", []string{"reviewer"}},
	}
	for _, tc := range cases {
		it := CompileIntent(tc.intent).Type
		got := CapabilitiesToAgents(DefaultCapabilities(it))
		if len(got) != len(tc.want) {
			t.Errorf("%s: agents=%v, want %v", tc.intent, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: agents=%v, want %v", tc.intent, got, tc.want)
				break
			}
		}
	}
}

// TestRun verifies kern_run creates a task and returns a valid RunResult.
func TestRun(t *testing.T) {
	svc, _ := newTestTaskService(t)
	result, err := svc.Run("add caching to UserService")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TaskID == "" {
		t.Fatal("TaskID is empty")
	}
	if result.Intent.Type != domain.IntentCodeChange {
		t.Errorf("Intent.Type=%s, want CODE_CHANGE", result.Intent.Type)
	}
	if result.Workflow != domain.WorkflowSafeChange {
		t.Errorf("Workflow=%s, want B_SAFE_CHANGE", result.Workflow)
	}
	if len(result.Capabilities) == 0 {
		t.Fatal("no capabilities")
	}
	if len(result.Tools) == 0 {
		t.Fatal("no tools")
	}
	if result.NextAction == "" {
		t.Fatal("NextAction is empty")
	}
	// Verify the task was persisted.
	stored, err := svc.store.Get(result.TaskID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.State != domain.TaskCreated {
		t.Errorf("stored state=%s, want CREATED", stored.State)
	}
}

// TestRunHighRiskApproval verifies high-risk intents set approval required.
func TestRunHighRiskApproval(t *testing.T) {
	svc, _ := newTestTaskService(t)
	result, err := svc.Run("deploy version 2.0 to production")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Risk.Level != domain.RiskHigh {
		t.Errorf("Risk.Level=%s, want HIGH", result.Risk.Level)
	}
	if result.ApprovalState != "required" {
		t.Errorf("ApprovalState=%s, want required", result.ApprovalState)
	}
}

// TestContextPlanIntentAware verifies the RunResult context plan is derived
// from the intent's capabilities rather than a hardcoded universal string:
// a WHAT_IF run shows its capability sequence, a CODE_CHANGE run shows the full
// canonical execution order.
func TestContextPlanIntentAware(t *testing.T) {
	if got := contextPlanFor(domain.IntentWhatIf); got != "whatif" {
		t.Errorf("WHAT_IF context plan = %q, want %q", got, "whatif")
	}
	if got := contextPlanFor(domain.IntentCodeChange); got != "analyze → context → memory → impact → risk → plan → execute → verify → pr" {
		t.Errorf("CODE_CHANGE context plan = %q", got)
	}
	if got := contextPlanFor(domain.IntentDeploy); got != "deploy" {
		t.Errorf("DEPLOY context plan = %q, want deploy", got)
	}
	// Run surfaces the derived plan on the RunResult.
	svc, _ := newTestTaskService(t)
	res, err := svc.Run("what if we remove redis")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ContextPlan != "whatif" {
		t.Errorf("RunResult.ContextPlan = %q, want %q", res.ContextPlan, "whatif")
	}
}
