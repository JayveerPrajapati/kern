package orchestrate

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
// suite uses — so the happy path exercises the real silent pipeline.
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

func TestOrchestrateHappyPath(t *testing.T) {
	h, root := newFixtureHooks(t)
	out, err := Orchestrate(context.Background(), h, map[string]any{
		"root": root, "intent": "fix NewServer", "budget": "4000",
	})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("Orchestrate output is not valid JSON: %v\n%s", err, out)
	}
	tt, _ := m["task_type"].(string)
	if tt == "" {
		t.Errorf("task_type missing or empty in result: %s", out)
	}
}

func TestOrchestrateIntentRequired(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := Orchestrate(context.Background(), h, map[string]any{"root": root}); err == nil {
		t.Fatal("expected error for missing intent")
	} else if !strings.Contains(err.Error(), "intent is required") {
		t.Fatalf("err = %v, want 'intent is required'", err)
	}
	// Zero-value Hooks: the intent check must fire before any hook call.
	if _, err := Orchestrate(context.Background(), Hooks{}, nil); err == nil {
		t.Fatal("expected error for missing intent with nil args")
	}
}

func TestOrchestrateBadBudget(t *testing.T) {
	called := false
	h := Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			called = true
			return nil, errors.New("should not be reached")
		},
	}
	if _, err := Orchestrate(context.Background(), h, map[string]any{
		"intent": "fix NewServer", "budget": "lots",
	}); err == nil {
		t.Fatal("expected error for malformed budget")
	}
	if called {
		t.Fatal("PlatformFor called despite malformed budget (parse must precede hook)")
	}
}

func TestOrchestratePlatformError(t *testing.T) {
	spyErr := errors.New("platform unavailable")
	h := Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			return nil, spyErr
		},
	}
	_, err := Orchestrate(context.Background(), h, map[string]any{"intent": "fix NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
}

func TestOrchestrateDefaultRoot(t *testing.T) {
	spyErr := errors.New("platform unavailable")
	var gotRoot string
	h := Hooks{
		PlatformFor: func(_ context.Context, root string) (*app.Platform, error) {
			gotRoot = root
			return nil, spyErr
		},
	}
	_, err := Orchestrate(context.Background(), h, map[string]any{"intent": "fix NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
	if gotRoot != "." {
		t.Fatalf("default root = %q, want \".\"", gotRoot)
	}
}
