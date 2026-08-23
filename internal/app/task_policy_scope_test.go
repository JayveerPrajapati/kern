package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestUnifiedTaskPolicyScope verifies the Phase 7.3 unified task policy: one
// TaskScope gates context, memory, artifacts, and runtime uniformly. A value
// denied for `context` must be denied for `memory`, `artifact`, and `runtime`
// under the SAME task and policy — there is exactly one boundary, not four.
func TestUnifiedTaskPolicyScope(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)

	// Register a single task scope. Any value under the denied path is outside
	// the boundary regardless of the resource kind it is accessed through.
	ts.SetTaskScope("task-1", domain.TaskScope{
		TaskID:      "task-1",
		Paths:       []string{"src/"},
		DeniedPaths: []string{"src/secret/"},
		Envs:        []string{"development"},
	})

	kinds := []string{"context", "memory", "artifact", "runtime"}

	// A value denied for context is denied for every resource kind, and every
	// denial names the SAME boundary (the task scope), not a per-kind policy.
	denied := "src/secret/creds.go"
	for _, kind := range kinds {
		ok, reason := ts.authorizeResource(context.Background(), "task-1", kind, "read", denied)
		if ok {
			t.Errorf("%s: authorizeResource(%q) allowed, want deny", kind, denied)
		}
		if reason == "" {
			t.Errorf("%s: expected a deny reason for %q", kind, denied)
		}
		if !strings.Contains(reason, "outside the task scope") {
			t.Errorf("%s: reason %q does not name the shared task boundary", kind, reason)
		}
	}

	// A value inside the scope is allowed across all resource kinds.
	allowed := "src/services/util.go"
	for _, kind := range kinds {
		ok, _ := ts.authorizeResource(context.Background(), "task-1", kind, "read", allowed)
		if !ok {
			t.Errorf("%s: authorizeResource(%q) denied, want allow", kind, allowed)
		}
	}

	// The environment gate applies uniformly too: an action that targets an
	// out-of-scope environment is denied for every resource kind.
	for _, kind := range kinds {
		ok, _ := ts.authorizeResource(context.Background(), "task-1", kind, "read:production", allowed)
		if ok {
			t.Errorf("%s: authorizeResource(read:production) allowed, want deny", kind)
		}
	}
}
