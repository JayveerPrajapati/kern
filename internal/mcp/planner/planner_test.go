package planner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// newFixtureHooks builds Hooks backed by a real Platform over the tiny
// testfixture repo — the same offline fixture the internal/mcp handler
// suite uses — so the happy paths exercise the real Analyze pipeline.
func newFixtureHooks(t *testing.T) (Hooks, string) {
	t.Helper()
	t.Setenv("KERN_PRELOAD", "0")
	root := testfixture.Repo(t)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	return Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			return p, nil
		},
	}, root
}

func TestPlanContextTextMode(t *testing.T) {
	h, root := newFixtureHooks(t)
	out, err := PlanContext(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer",
	})
	if err != nil {
		t.Fatalf("PlanContext: %v", err)
	}
	if !strings.HasPrefix(out, "== context plan:") {
		t.Fatalf("text plan missing header, got: %s", out)
	}
	if !strings.Contains(out, "NewServer") {
		t.Errorf("text plan missing change mention, got: %s", out)
	}
}

func TestPlanContextJSONMode(t *testing.T) {
	h, root := newFixtureHooks(t)
	out, err := PlanContext(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "json": true,
	})
	if err != nil {
		t.Fatalf("PlanContext(json): %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("PlanContext output is not valid JSON: %v\n%s", err, out)
	}
	tt, _ := m["task_type"].(string)
	if tt == "" {
		t.Errorf("task_type missing or empty in plan: %s", out)
	}
	if b, _ := m["budget"].(float64); b <= 0 {
		t.Errorf("budget = %v, want > 0 (policy default)", m["budget"])
	}
}

func TestPlanContextChangeRequired(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := PlanContext(context.Background(), h, map[string]any{"root": root}); err == nil {
		t.Fatal("expected error for missing change")
	} else if !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("err = %v, want 'change is required'", err)
	}
	// Zero-value Hooks: the change check must fire before any hook call.
	if _, err := PlanContext(context.Background(), Hooks{}, nil); err == nil {
		t.Fatal("expected error for missing change with nil args")
	}
}

func TestPlanContextPlatformError(t *testing.T) {
	spyErr := errors.New("platform unavailable")
	h := Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			return nil, spyErr
		},
	}
	_, err := PlanContext(context.Background(), h, map[string]any{"change": "NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
}

func TestPlanContextAnalyzeError(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := PlanContext(context.Background(), h, map[string]any{
		"root": root, "change": "DefinitelyNotASymbolZZZ",
	}); err == nil {
		t.Fatal("expected error for unresolvable change")
	}
}

func TestPlanContextBadBudget(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := PlanContext(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "budget": "lots",
	}); err == nil {
		t.Fatal("expected error for malformed budget")
	}
}

func TestPlanContextDefaultRoot(t *testing.T) {
	spyErr := errors.New("platform unavailable")
	var gotRoot string
	h := Hooks{
		PlatformFor: func(_ context.Context, root string) (*app.Platform, error) {
			gotRoot = root
			return nil, spyErr
		},
	}
	_, err := PlanContext(context.Background(), h, map[string]any{"change": "NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
	if gotRoot != "." {
		t.Fatalf("default root = %q, want \".\"", gotRoot)
	}
}
