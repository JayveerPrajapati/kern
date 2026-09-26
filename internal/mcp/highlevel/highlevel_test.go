package highlevel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// The highlevel handlers are thin TaskService routings, so their pure unit
// surface is the argument validation and error mapping that runs BEFORE any
// injected hook is consulted — a nil Hooks bundle must therefore be enough
// to exercise the rejection paths below. Fixture-based integration tests
// (real index + Server) stay in the root mcp package.

func TestAnalyzeRequiresChange(t *testing.T) {
	_, err := Analyze(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

func TestPlanRequiresChange(t *testing.T) {
	_, err := Plan(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

func TestExecuteRequiresPatch(t *testing.T) {
	_, err := Execute(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "patch is required") {
		t.Fatalf("missing patch: got err %v, want rejection with 'patch is required'", err)
	}
}

func TestWhatIfRequiresChange(t *testing.T) {
	_, err := WhatIf(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

func TestImpactRequiresChange(t *testing.T) {
	_, err := Impact(context.Background(), Hooks{}, map[string]any{"root": ".", "risk": "true"})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

func TestRunRequiresIntent(t *testing.T) {
	_, err := Run(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "intent is required") {
		t.Fatalf("missing intent: got err %v, want rejection with 'intent is required'", err)
	}
}

func TestLoopRequiresIntent(t *testing.T) {
	_, err := Loop(context.Background(), Hooks{}, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "intent is required") {
		t.Fatalf("missing intent: got err %v, want rejection with 'intent is required'", err)
	}
}

func TestIncidentRejectsInvalidAlertJSON(t *testing.T) {
	_, err := Incident(context.Background(), Hooks{}, map[string]any{"root": ".", "alert": "not-json"})
	if err == nil || !strings.Contains(err.Error(), "invalid alert JSON") {
		t.Fatalf("invalid alert: got err %v, want rejection with 'invalid alert JSON'", err)
	}
}

func TestCorrelateRejectsInvalidAlertJSON(t *testing.T) {
	_, err := Correlate(context.Background(), Hooks{}, map[string]any{"root": ".", "alert": "not-json"})
	if err == nil || !strings.Contains(err.Error(), "invalid alert JSON") {
		t.Fatalf("invalid alert: got err %v, want rejection with 'invalid alert JSON'", err)
	}
}

func TestLearnRejectsInvalidThreshold(t *testing.T) {
	_, err := Learn(context.Background(), Hooks{}, map[string]any{"root": ".", "threshold": "abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("invalid threshold: got err %v, want rejection with 'invalid integer'", err)
	}
}

// TestVerifyRejectsUnknownType pins the up-front verify type gate at the leaf
// level: types=123 (a number coerced to the string "123") must be rejected
// before any hook is consulted — never a vacuous "summary: PASS" run.
func TestVerifyRejectsUnknownType(t *testing.T) {
	_, err := Verify(context.Background(), Hooks{}, map[string]any{"root": ".", "types": "123"})
	if err == nil {
		t.Fatal("Verify(types=123) must error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown verify type: 123") {
		t.Fatalf("error must name the offending type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "known:") {
		t.Fatalf("error must list the known types, got: %v", err)
	}
	// A garbage token mixed with valid ones is rejected too, and the valid
	// token is never silently dropped.
	_, err = Verify(context.Background(), Hooks{}, map[string]any{"root": ".", "types": "build,zzz"})
	if err == nil || !strings.Contains(err.Error(), "unknown verify type: zzz") {
		t.Fatalf("Verify(types=build,zzz): err = %v, want rejection of zzz", err)
	}
}

// TestApproveListsNoPendingWithStub drives the empty list path of Approve
// through the injected hook: a stub returning no approvals must yield the
// "no pending approvals" contract line.
func TestApproveListsNoPendingWithStub(t *testing.T) {
	h := Hooks{
		PendingApprovals: func(ctx context.Context, root string) ([]domain.Approval, error) {
			return nil, nil
		},
	}
	out, err := Approve(context.Background(), h, map[string]any{"root": "."})
	if err != nil {
		t.Fatalf("Approve (list, empty): %v", err)
	}
	if out != "no pending approvals" {
		t.Errorf("Approve (list, empty) = %q, want %q", out, "no pending approvals")
	}
}

// TestProbeLLMProviderReachableFailsWithoutProvider exercises the extracted
// pre-flight helper directly: with the provider pinned to an unreachable
// Ollama address it errors quickly (the probe is bounded ~8s), which is the
// condition that used to send kern_loop mode=autonomous down the 180s path.
func TestProbeLLMProviderReachableFailsWithoutProvider(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	start := time.Now()
	err := ProbeLLMProviderReachable()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("probe must fail with no reachable provider")
	}
	if elapsed > 30*time.Second {
		t.Errorf("probe took %v — must be bounded and fast", elapsed)
	}
}
