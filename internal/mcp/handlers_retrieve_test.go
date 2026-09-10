package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestHandleRetrieveTaskType drives the task_type arg through kern_retrieve:
// the disclosure level comes from the planner policy for the task type
// (documentation→l1, refactor→l3, everything else l2), and level/task_type are
// mutually exclusive.
func TestHandleRetrieveTaskType(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	args := func(extra map[string]any) map[string]any {
		m := map[string]any{"root": root, "symbol": "Greet"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	ref, err := s.handleRetrieve(context.Background(), args(map[string]any{"task_type": "refactor"}))
	if err != nil {
		t.Fatalf("handleRetrieve task_type=refactor: %v", err)
	}
	if !strings.Contains(ref, "== level 3:") {
		t.Errorf("refactor output missing level-3 header:\n%s", ref)
	}
	if !strings.Contains(ref, "Greet") {
		t.Errorf("refactor output missing symbol:\n%s", ref)
	}

	doc, err := s.handleRetrieve(context.Background(), args(map[string]any{"task_type": "documentation"}))
	if err != nil {
		t.Fatalf("handleRetrieve task_type=documentation: %v", err)
	}
	if !strings.Contains(doc, "== level 1:") {
		t.Errorf("documentation output missing level-1 header:\n%s", doc)
	}

	fix, err := s.handleRetrieve(context.Background(), args(map[string]any{"task_type": "fix_bug"}))
	if err != nil {
		t.Fatalf("handleRetrieve task_type=fix_bug: %v", err)
	}
	if !strings.Contains(fix, "== level 2:") {
		t.Errorf("fix_bug output missing level-2 header:\n%s", fix)
	}

	if _, err := s.handleRetrieve(context.Background(), args(map[string]any{"level": "l2", "task_type": "refactor"})); err == nil || !strings.Contains(err.Error(), "use only one of") {
		t.Errorf("level+task_type err = %v, want mutual-exclusion error", err)
	}

	if _, err := s.handleRetrieve(context.Background(), args(map[string]any{"task_type": "refactor", "symbol": "NoSuchSymbol"})); err == nil || !strings.Contains(err.Error(), "not found in index") {
		t.Errorf("unknown symbol err = %v, want not-found error", err)
	}
}
