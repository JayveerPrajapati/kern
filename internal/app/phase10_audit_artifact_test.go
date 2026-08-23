package app

import (
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestAnalyzeRecordsAnalysisReport verifies that the Analyze workflow records a
// typed AnalysisReport artifact linked as a child of the context-packet artifact
// (Phase 10.4).
func TestAnalyzeRecordsAnalysisReport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module analyzep10\n\ngo 1.21\n")
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

	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}

	var analysis, contextPkt *domain.Artifact
	for i := range arts {
		a := &arts[i]
		switch a.Kind {
		case domain.ArtifactAnalysisReport:
			analysis = a
		case domain.ArtifactContextPacket:
			contextPkt = a
		}
	}
	if analysis == nil {
		t.Fatal("analysis_report artifact not recorded by Analyze")
	}
	if contextPkt == nil {
		t.Fatal("context_packet artifact not recorded by Analyze")
	}
	if analysis.ParentArtifactID != contextPkt.ID {
		t.Errorf("analysis_report parent = %q, want context_packet %q", analysis.ParentArtifactID, contextPkt.ID)
	}
	if analysis.Provenance != "context:analyze" {
		t.Errorf("analysis_report provenance = %q, want context:analyze", analysis.Provenance)
	}
}

// TestObserveRecordsAuditArtifact verifies that when a task completes through
// the Observe finalize point, an audit artifact is recorded (Phase 10.4).
func TestObserveRecordsAuditArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module auditp10\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, err := ts.Create("NewServer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Analyze without completing to keep the lifecycle alive.
	task, _, err = ts.analyzeTaskOpts(task, "NewServer", false)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// Drive the task to DEPLOYING (the only valid precursor to OBSERVING),
	// then observe to finalize and record the audit artifact.
	for _, next := range []domain.TaskState{
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskReadyForPR,
		domain.TaskPRCreated,
		domain.TaskDeploying,
	} {
		if err := task.Transition(next); err != nil {
			t.Fatalf("transition %s: %v", next, err)
		}
	}

	if _, err := ts.Observe(task.ID); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	var audit *domain.Artifact
	for i := range arts {
		if arts[i].Kind == domain.ArtifactAudit {
			a := &arts[i]
			audit = a
			if a.CreatedBy != "audit-engine" {
				t.Errorf("audit artifact createdBy = %q, want audit-engine", a.CreatedBy)
			}
			if a.Provenance != "audit:finalize" {
				t.Errorf("audit artifact provenance = %q, want audit:finalize", a.Provenance)
			}
		}
	}
	if audit == nil {
		t.Fatal("audit artifact not recorded after task completion")
	}
}