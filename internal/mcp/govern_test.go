package mcp

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestNewGovernator_PermissiveRawMode(t *testing.T) {
	t.Setenv("KERN_MCP_PERMISSIVE", "1")
	root := provenanceProject(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	gov, err := (&Server{}).newGovernor(context.Background(), map[string]any{"root": root}, ix)
	if err != nil {
		t.Fatalf("permissive mode must not error: %v", err)
	}
	if gov != nil {
		t.Fatalf("permissive mode must return a nil governor, got %+v", gov)
	}
}

// TestNewGovernator_DefaultScoped: with no KERN_MCP_PERMISSIVE and no
// agent_id, the governor is non-nil, policySource is "default-scoped", and the
// allowed set is confined to the project root symbols (the whole fixture
// project is the cwd scope).
func TestNewGovernator_DefaultScoped(t *testing.T) {
	governance.EnsureDefaultAgent() // NewServer does this at init; these tests call the handler directly
	root := provenanceProject(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	gov, err := (&Server{}).newGovernor(context.Background(), map[string]any{"root": root}, ix)
	if err != nil {
		t.Fatalf("default-governed call must not error: %v", err)
	}
	if gov == nil {
		t.Fatal("default path must return a non-nil governor")
	}
	if gov.PolicySource != policySourceDefaultScoped {
		t.Fatalf("expected policySource %q, got %q", policySourceDefaultScoped, gov.PolicySource)
	}
	// The whole project root is in scope: PublicA, Greet, main, SecretB.
	for _, want := range []string{"PublicA", "Greet", "main", "SecretB"} {
		if !gov.Allowed[want] {
			t.Errorf("default scope must allow project symbol %s (allowed=%v)", want, gov.Allowed)
		}
	}
}

// TestNewGovernator_ExplicitAgentTaskScope: an explicit agent_id + task +
// scope yields policySource "task-scope" (existing governed behavior
// preserved), with the denied path excluded from the allowed set.
func TestNewGovernator_ExplicitAgentTaskScope(t *testing.T) {
	agent := registerP12Agent(t)
	root := provenanceProject(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	args := map[string]any{
		"root":     root,
		"agent_id": agent,
		"task":     "T-govern-test",
		"scope":    deniedScope(),
	}
	gov, err := (&Server{}).newGovernor(context.Background(), args, ix)
	if err != nil {
		t.Fatalf("explicit governed call must not error: %v", err)
	}
	if gov == nil {
		t.Fatal("explicit governed call must return a non-nil governor")
	}
	if gov.PolicySource != policySourceTaskScope {
		t.Fatalf("expected policySource %q, got %q", policySourceTaskScope, gov.PolicySource)
	}
	if !gov.Allowed["PublicA"] || gov.Allowed["SecretB"] {
		t.Fatalf("task-scope governor allowed set wrong: PublicA must be allowed and SecretB denied (allowed=%v)", gov.Allowed)
	}
}

// TestNewGovernator_UnknownAgentDenied: an explicit but unregistered agent_id
// fails closed with an error. The default agent is registered at server init,
// so this exercises an explicitly-unknown agent; the governor is still
// returned alongside the error so the denial is auditable.
func TestNewGovernator_UnknownAgentDenied(t *testing.T) {
	root := provenanceProject(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	gov, err := (&Server{}).newGovernor(context.Background(), map[string]any{
		"root":     root,
		"agent_id": "no-such-agent-xyz",
	}, ix)
	if err == nil {
		t.Fatal("unknown agent must fail closed with an error")
	}
	if gov == nil {
		t.Fatal("the governor must still be returned alongside the error for auditability")
	}
	if gov.PolicySource == "" {
		t.Fatal("denied governor must carry its policy source")
	}
}
