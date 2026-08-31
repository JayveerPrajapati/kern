package mcp

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestWorkflowToolRunsTeam verifies the exit gate through the MCP
// surface: kern_workflow selects and coordinates the agent team from an intent
// alone, parks at the human approval gate with an approval ID, and — after the
// gate is resolved out-of-band via the persistent store — kern_workflow with
// the same task_id resumes and completes the run.
func TestWorkflowToolRunsTeam(t *testing.T) {
	root := mcpProject(t)

	// Run 1: kern sequences the team; the run must park at the approval gate
	// and surface the approval ID + task ID.
	out := mcpLastOK(t, "kern_workflow", map[string]any{"root": root, "intent": "Greet"})
	if !strings.Contains(out, "approval required:") {
		t.Fatalf("output missing approval gate:\n%s", out)
	}
	approvalID := extractBetween(out, "approval required: ", "\n")
	taskID := extractBetween(out, "task:     ", "\n")
	if approvalID == "" || taskID == "" {
		t.Fatalf("could not extract approval/task id from:\n%s", out)
	}

	// Out-of-band approval: the same persistent store `kern approve` writes.
	store := governance.NewFileStore(root)
	if _, err := store.Decide(approvalID, "test-human", true, ""); err != nil {
		t.Fatalf("store.Decide: %v", err)
	}

	// Resume with the same task_id (fresh handler path): the run completes.
	out = mcpLastOK(t, "kern_workflow", map[string]any{"root": root, "task_id": taskID})
	if !strings.Contains(out, "COMPLETED") {
		t.Fatalf("resumed run did not complete:\n%s", out)
	}
	for _, want := range []string{"code", "verify", "pr"} {
		if !strings.Contains(out, want) {
			t.Errorf("resumed output missing stage %q:\n%s", want, out)
		}
	}
}

// extractBetween returns the substring between start and end markers.
func extractBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:j])
}
