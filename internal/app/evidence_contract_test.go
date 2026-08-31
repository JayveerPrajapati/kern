package app

import (
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestAnalyzeAttachesEvidenceClaims verifies the evidence contract:
// the context engine's evidence-backed claims (FACT/INFERENCE/HYPOTHESIS/
// RECOMMENDATION) are attached to the Task's Evidence field and persisted with
// it, so the analysis is auditable from the Task alone (not only from the
// rendered text or the artifact chain). This closes the gap where the
// agent.Task.Evidence field was declared and rendered but never populated.
func TestAnalyzeAttachesEvidenceClaims(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module evidencep3\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(task.Evidence) == 0 {
		t.Fatal("Analyze did not attach any evidence claims to task.Evidence")
	}

	// Every claim must carry a valid spec claim type and a statement.
	for _, c := range task.Evidence {
		if c.Statement == "" {
			t.Errorf("evidence claim with empty statement: %+v", c)
		}
		switch c.Type {
		case domain.ClaimFact, domain.ClaimInference, domain.ClaimHypothesis, domain.ClaimRecommendation:
		default:
			t.Errorf("evidence claim has unknown claim type %q: %+v", c.Type, c)
		}
	}

	// The claims must be persisted with the task, not only in-memory.
	loaded, ok := ts.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not queryable after Analyze", task.ID)
	}
	if len(loaded.Evidence) != len(task.Evidence) {
		t.Errorf("persisted evidence claims = %d, want %d (claims lost across store round trip)",
			len(loaded.Evidence), len(task.Evidence))
	}
}
