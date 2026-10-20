package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestExploreBudgetDeniesThirdCall (B2): KERN_EXPLORE_BUDGET=2 caps
// explore-phase calls per session — calls 1-2 pass, the 3rd is denied with
// the exact teaching message. A non-explore tool (kern_mask_pii, cross
// phase) is unaffected by the exhausted explore budget. A subtest sets the
// safety-budget env too, proving the phase denial keeps its exact message
// even when the generic gateway budget coexists (the mirror into
// TrackToolCallPhase must not change the teaching text).
func TestExploreBudgetDeniesThirdCall(t *testing.T) {
	t.Setenv("KERN_EXPLORE_BUDGET", "2")
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}

	for i := 1; i <= 2; i++ {
		out, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs)
		if err != nil {
			t.Fatalf("call %d must pass under the explore cap, got: %v", i, err)
		}
		if out == "" {
			t.Fatalf("call %d produced an empty result", i)
		}
	}

	_, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs)
	if err == nil {
		t.Fatal("third explore call must be denied")
	}
	// The denial wraps the ErrToolDenied sentinel (convention: every
	// precheckTool denial renders as "tool call denied: <reason>"); the
	// teaching text is the reason, verbatim.
	if !strings.Contains(err.Error(), "tool call denied") || !errors.Is(err, domain.ErrToolDenied) {
		t.Fatalf("denial must be a structured ErrToolDenied, got: %v", err)
	}
	want := "explore budget exceeded (2 calls) — emit a plan (kern_plan) or raise KERN_EXPLORE_BUDGET"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("denial message = %q, want it to contain %q", err.Error(), want)
	}

	// A non-explore tool (cross phase) is unaffected after the explore cap is
	// exhausted.
	out, err := s.runTool(context.Background(), "t", "", "kern_mask_pii", map[string]any{"text": "hello bob@example.com"})
	if err != nil {
		t.Fatalf("non-explore tool must pass after explore denials, got: %v", err)
	}
	if out == "" {
		t.Fatal("non-explore tool produced an empty result")
	}
}

// TestExploreBudgetCoexistsWithSafetyBudget pins the coexistence contract:
// when the operator sets BOTH KERN_SAFETY_BUDGET_* and KERN_EXPLORE_BUDGET,
// the phase denial still surfaces the exact teaching message (the generic
// gateway budget must not intercept it with its own wording).
func TestExploreBudgetCoexistsWithSafetyBudget(t *testing.T) {
	t.Setenv("KERN_EXPLORE_BUDGET", "2")
	t.Setenv("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS", "100")
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	if s.budget == nil {
		t.Fatal("safety-budget env must wire s.budget")
	}

	for i := 1; i <= 2; i++ {
		if _, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs); err != nil {
			t.Fatalf("call %d must pass, got: %v", i, err)
		}
	}
	_, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs)
	if err == nil {
		t.Fatal("third explore call must be denied")
	}
	want := "explore budget exceeded (2 calls) — emit a plan (kern_plan) or raise KERN_EXPLORE_BUDGET"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("denial message = %q, want it to contain the teaching text %q", err.Error(), want)
	}
}

// TestExploreBudgetUnsetNoCap: without KERN_EXPLORE_BUDGET the phase budget
// is completely off — explore calls are never capped.
func TestExploreBudgetUnsetNoCap(t *testing.T) {
	t.Setenv("KERN_EXPLORE_BUDGET", "") // empty == unset for the parser
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	if s.phaseBudgetCap != 0 {
		t.Fatalf("unset KERN_EXPLORE_BUDGET must leave the cap at 0, got %d", s.phaseBudgetCap)
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs); err != nil {
			t.Fatalf("call %d must pass with no cap, got: %v", i, err)
		}
	}
}

// TestExploreBudgetMalformedEnvDisabled: a malformed KERN_EXPLORE_BUDGET
// warns on stderr (fails-loud) and disables the cap.
func TestExploreBudgetMalformedEnvDisabled(t *testing.T) {
	t.Setenv("KERN_EXPLORE_BUDGET", "not-a-number")
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	_ = w.Close()
	os.Stderr = old
	warn, _ := io.ReadAll(r)
	if !strings.Contains(string(warn), "KERN_EXPLORE_BUDGET") {
		t.Fatalf("malformed env must warn on stderr, got %q", warn)
	}
	s.roots = []string{"/"}
	if s.phaseBudgetCap != 0 {
		t.Fatalf("malformed KERN_EXPLORE_BUDGET must disable the cap, got %d", s.phaseBudgetCap)
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.runTool(context.Background(), "t", "", "kern_compact_file", compactArgs); err != nil {
			t.Fatalf("call %d must pass with the cap disabled, got: %v", i, err)
		}
	}
}

// TestExploreBudgetPrecheckToolNoTrackingOnDeny verifies at the choke-point
// level (precheckTool) that a denied explore call neither executes nor
// consumes budget, and that non-explore tools are never counted.
func TestExploreBudgetPrecheckToolNoTrackingOnDeny(t *testing.T) {
	t.Setenv("KERN_EXPLORE_BUDGET", "1")
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	key := sessionKeyFor(compactArgs)

	name, err := s.precheckTool("kern_compact_file", compactArgs)
	if err != nil {
		t.Fatalf("first precheck must pass: %v", err)
	}
	if name != "kern_compact_file" {
		t.Fatalf("precheck remapped name to %q", name)
	}
	if s.phaseBudget[key] != 1 {
		t.Fatalf("allowed explore precheck must count one call, got %d", s.phaseBudget[key])
	}

	if _, err := s.precheckTool("kern_compact_file", compactArgs); err == nil {
		t.Fatal("second explore precheck must be denied when the cap is exhausted")
	}
	if s.phaseBudget[key] != 1 {
		t.Fatalf("denied precheck must not consume budget, got %d", s.phaseBudget[key])
	}

	// A non-explore tool passes and is never counted against the explore cap.
	if _, err := s.precheckTool("kern_mask_pii", map[string]any{"text": "hello"}); err != nil {
		t.Fatalf("non-explore precheck must pass: %v", err)
	}
	if s.phaseBudget[key] != 1 {
		t.Fatalf("non-explore tool must not consume the explore budget, got %d", s.phaseBudget[key])
	}
}
