package tasklife

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

func TestVerifyWarnOutputListsSuppressedFindings(t *testing.T) {
	root := t.TempDir()
	p := &cannedVerdictPlatform{
		testPlatform: &testPlatform{root: root, ver: verification.NewEngine(root)},
		res: verdict.VerificationResult{
			Verdict: verdict.VerdictWarn,
			Summary: "security: 1 finding, 1 suppressed (all triaged)",
			Security: &verdict.SecurityResult{
				OK:         true,
				Count:      1,
				Suppressed: 1,
				Findings: []verdict.Finding{{
					File: "sql.go", Line: 3, Rule: "sql-injection", Severity: "high",
					Message: "dynamic SQL", Suppressed: true,
					SuppressionReason: "constant query shape",
				}},
			},
		},
	}
	task, _, err := NewTaskService(p, nil).Verify([]string{"security"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if task.State != domain.TaskCompleted {
		t.Fatalf("task state = %q, want COMPLETED", task.State)
	}
	for _, want := range []string{"verdict: WARN", "sql.go:3", "[suppressed]", "constant query shape"} {
		if !strings.Contains(task.Output, want) {
			t.Errorf("task output missing %q:\n%s", want, task.Output)
		}
	}
}

func TestVerifyPassOutputStaysShort(t *testing.T) {
	root := t.TempDir()
	p := &cannedVerdictPlatform{
		testPlatform: &testPlatform{root: root, ver: verification.NewEngine(root)},
		res:          verdict.VerificationResult{Verdict: verdict.VerdictPass, Summary: "build: PASS"},
	}
	task, _, err := NewTaskService(p, nil).Verify([]string{"build"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if task.Output != "verdict: PASS\nsummary: build: PASS" {
		t.Errorf("PASS output grew: %q", task.Output)
	}
}
