package tasklife

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestAssemblePlanExpectedTestCommandAndApproval(t *testing.T) {
	ts := &TaskService{}
	pkt := domain.ContextPacket{
		Files: []domain.File{
			{Path: "internal/tasklife/render.go"},
			{Path: "internal/tasklife/task_analysis.go"},
			{Path: "internal/domain/domain.go"},
		},
		Risks: []domain.Risk{{Level: domain.RiskHigh, ApprovalRequired: true}},
	}
	plan := ts.assemblePlan("Change the plan contract", pkt)
	if want := "go test -count=1 ./internal/domain ./internal/tasklife"; plan.ExpectedTestCommand != want {
		t.Errorf("ExpectedTestCommand = %q, want %q", plan.ExpectedTestCommand, want)
	}
	if want := "approval required before governed execution"; plan.ApprovalRequirement != want {
		t.Errorf("ApprovalRequirement = %q, want %q", plan.ApprovalRequirement, want)
	}
	rendered := renderPlanText(plan)
	for _, want := range []string{
		"Expected test command: go test -count=1 ./internal/domain ./internal/tasklife",
		"Approval requirement: approval required before governed execution",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan missing %q:\n%s", want, rendered)
		}
	}
}

func TestAssemblePlanExpectedTestCommandCollapsesLargePackageSet(t *testing.T) {
	ts := &TaskService{}
	pkt := domain.ContextPacket{Files: []domain.File{
		{Path: "a/a.go"}, {Path: "b/b.go"}, {Path: "c/c.go"},
		{Path: "d/d.go"}, {Path: "e/e.go"}, {Path: "f/f.go"},
	}}
	plan := ts.assemblePlan("Change six packages", pkt)
	if want := "go test -count=1 ./..."; plan.ExpectedTestCommand != want {
		t.Errorf("ExpectedTestCommand = %q, want %q", plan.ExpectedTestCommand, want)
	}
}
