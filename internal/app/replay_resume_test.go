package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestReplayTaskMetadata verifies replay metadata (repo version,
// model, config hash) is captured and the reconstructed task carries context.
func TestReplayTaskMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	rec, err := ts.ReplayTask(task.ID, "abc1234", "llama3.1:8b", "cfg-1")
	if err != nil {
		t.Fatalf("ReplayTask: %v", err)
	}
	if rec.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", rec.TaskID, task.ID)
	}
	if rec.RepoVersion != "abc1234" && rec.RepoVersion != "abc123" {
		t.Errorf("RepoVersion = %q, want the passed repo version", rec.RepoVersion)
	}
	if rec.Model != "llama3.1:8b" || rec.ConfigHash != "cfg-1" {
		t.Errorf("ReplayRecord = %+v", rec)
	}
	if rec.ReplayedAt.IsZero() {
		t.Error("ReplayedAt not set")
	}
}

// TestRunCompare verifies run-compare reports artifact/state
// differences between two task runs.
func TestRunCompare(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	t1, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze 1: %v", err)
	}
	t2, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze 2: %v", err)
	}

	cmp, err := ts.RunCompare(t1.ID, t2.ID)
	if err != nil {
		t.Fatalf("RunCompare: %v", err)
	}
	if cmp.ArtifactDiff == nil {
		t.Error("ArtifactDiff should be populated")
	}
	if strings.TrimSpace(cmp.State1) == "" || strings.TrimSpace(cmp.State2) == "" {
		t.Errorf("states not captured: %q vs %q", cmp.State1, cmp.State2)
	}
}

// TestResumeReconstructsContext verifies resuming a task
// reconstructs its ContextPacket so the resumed task is not a shell.
func TestResumeReconstructsContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// Create a fresh task and pause it while it is still non-terminal, so the
	// resume path reconstructs context from a non-completed task.
	paused, err := ts.Create("pause me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Move to ANALYZING so the BLOCKED → ANALYZING resume transition is valid.
	if err := paused.Transition(domain.TaskAnalyzing); err != nil {
		t.Fatalf("Transition ANALYZING: %v", err)
	}
	if err := ts.Pause(paused.ID, "test pause"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	resumed, err := ts.Resume(paused.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// After a persisted-then-reloaded resume, context is reconstructed.
	if resumed.ContextPacket == nil {
		t.Error("resumed task should have a reconstructed ContextPacket (16.2)")
	}
	_ = task
}

// TestResumeReconstructsRichSnapshotContext verifies richer
// reconstruction: resuming a task layers the persisted context snapshot (goal /
// decisions / constraints / risks) onto the reconstructed ContextPacket.
func TestResumeReconstructsRichSnapshotContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// Drive a task through analyze so it has a rich snapshot, then pause the
	// analyzed task's sibling path. We reuse a freshly-created task so the
	// resume path exercises snapshot-driven reconstruction.
	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// Analyze attaches a ContextPacket already; simulate the resumed task not
	// having one by creating a separate task with no packet and a persisted
	// snapshot carrying its intent.
	paused, err := ts.Create("enrich my context")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := paused.Transition(domain.TaskAnalyzing); err != nil {
		t.Fatalf("Transition ANALYZING: %v", err)
	}
	if err := ts.Pause(paused.ID, "test pause"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	resumed, err := ts.Resume(paused.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.ContextPacket == nil {
		t.Fatal("resumed task should have a reconstructed ContextPacket (16.2)")
	}
	// The snapshot's Goal (derived from the task intent) should have been used
	// as the packet's Task intent when the artifact path had none.
	if resumed.ContextPacket.Task == "" {
		t.Error("resumed ContextPacket should carry the snapshot goal/intent")
	}
	_ = task
}

// TestReplayTaskRecordsMetadataAndReplayRecord verifies ReplayTask
// records repo version, model, config hash, and a replayed-at timestamp.
func TestReplayTaskRecordsMetadataAndReplayRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rec, err := ts.ReplayTask(task.ID, "deadbeef", "qwen2.5-coder:14b", "cfg-abc")
	if err != nil {
		t.Fatalf("ReplayTask: %v", err)
	}
	if rec.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", rec.TaskID, task.ID)
	}
	if rec.RepoVersion != "deadbeef" {
		t.Errorf("RepoVersion = %q, want %q", rec.RepoVersion, "deadbeef")
	}
	if rec.Model != "qwen2.5-coder:14b" {
		t.Errorf("Model = %q, want qwen2.5-coder:14b", rec.Model)
	}
	if rec.ConfigHash != "cfg-abc" {
		t.Errorf("ConfigHash = %q, want cfg-abc", rec.ConfigHash)
	}
	// Context + tool versions must be derived from the task's own
	// context packet and step actions.
	if rec.ContextVersion == "" {
		t.Error("ContextVersion empty — the replayed context digest must be recorded (16.3)")
	}
	if len(rec.ContextVersion) != 16 {
		t.Errorf("ContextVersion = %q, want a 16-char digest", rec.ContextVersion)
	}
	if rec.ToolVersions == "" {
		t.Error("ToolVersions empty — the tool-surface digest must be recorded (16.3)")
	}
	if rec.ReplayedAt.IsZero() {
		t.Error("ReplayedAt should be set")
	}
}

// TestRunComparePopulatesRichDimensions verifies RunCompare
// populates the richer run dimensions (agent, tool-call proxy, cost, success).
func TestRunComparePopulatesRichDimensions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("agent-a")

	t1, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze 1: %v", err)
	}
	t2, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze 2: %v", err)
	}
	// Create does not populate Task.AgentID (only CreatedBy/Requester), so set
	// it explicitly to exercise the rich comparison dimension.
	t1.AgentID = "agent-a"
	t2.AgentID = "agent-b"

	cmp, err := ts.RunCompare(t1.ID, t2.ID)
	if err != nil {
		t.Fatalf("RunCompare: %v", err)
	}
	if cmp.Agent1 == "" || cmp.Agent2 == "" {
		t.Errorf("agents not populated: %q vs %q", cmp.Agent1, cmp.Agent2)
	}
	if cmp.Agent1 != "agent-a" || cmp.Agent2 != "agent-b" {
		t.Errorf("agents = %q vs %q, want agent-a vs agent-b", cmp.Agent1, cmp.Agent2)
	}
	if cmp.ToolCalls1 == 0 || cmp.ToolCalls2 == 0 {
		t.Errorf("tool-call proxies not populated: %d vs %d", cmp.ToolCalls1, cmp.ToolCalls2)
	}
	if !cmp.Success1 || !cmp.Success2 {
		t.Errorf("success flags not populated: %v vs %v", cmp.Success1, cmp.Success2)
	}
	if cmp.Cost1 <= 0 || cmp.Cost2 <= 0 {
		t.Errorf("cost proxies not populated: %v vs %v", cmp.Cost1, cmp.Cost2)
	}
}
