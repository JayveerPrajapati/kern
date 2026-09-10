package flight

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecorderPersistsAndLists(t *testing.T) {
	r := New(t.TempDir())

	recs := []Record{
		{AgentID: "agentA", TaskID: "task1", Action: "grep", Status: "ok", Timestamp: time.Now().Add(-2 * time.Minute)},
		{AgentID: "agentB", TaskID: "task2", Action: "read", Status: "error", Timestamp: time.Now().Add(-1 * time.Minute)},
		{AgentID: "agentA", TaskID: "task3", Action: "edit", Status: "blocked", Timestamp: time.Now()},
	}
	for _, rec := range recs {
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record(%+v): %v", rec, err)
		}
	}

	listed, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("List() returned %d records, want 3", len(listed))
	}
	// Most-recent-first.
	if listed[0].AgentID != "agentA" || listed[0].TaskID != "task3" {
		t.Errorf("most recent record = %+v, want task3", listed[0])
	}
	if listed[2].AgentID != "agentA" || listed[2].TaskID != "task1" {
		t.Errorf("oldest record = %+v, want task1", listed[2])
	}

	filtered := r.Filter("agentA", "", "")
	if len(filtered) != 2 {
		t.Fatalf("Filter(agentA) returned %d records, want 2", len(filtered))
	}
	for _, rec := range filtered {
		if rec.AgentID != "agentA" {
			t.Errorf("Filter(agentA) returned record with AgentID=%q", rec.AgentID)
		}
	}
}

func TestRecorderPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()

	a := New(dir)
	first, err := a.Record(Record{AgentID: "agentA", TaskID: "task1", Action: "grep", Status: "ok"})
	if err != nil {
		t.Fatalf("Record 1: %v", err)
	}
	second, err := a.Record(Record{AgentID: "agentB", TaskID: "task2", Action: "edit", Status: "denied"})
	if err != nil {
		t.Fatalf("Record 2: %v", err)
	}

	b := New(dir)
	listed, err := b.List()
	if err != nil {
		t.Fatalf("List on new instance: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() on new instance returned %d records, want 2", len(listed))
	}

	got := map[string]bool{}
	for _, rec := range listed {
		got[rec.ID] = true
	}
	if !got[first.ID] || !got[second.ID] {
		t.Errorf("new instance did not see persisted records: got IDs %v, want %q and %q", got, first.ID, second.ID)
	}
}

func TestRecordIDAndTimestampDefault(t *testing.T) {
	r := New(t.TempDir())

	rec, err := r.Record(Record{AgentID: "agentA", TaskID: "task1", Action: "read", Status: "ok"})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.ID == "" {
		t.Error("expected non-empty generated ID, got empty")
	}
	if rec.Timestamp.IsZero() {
		t.Error("expected non-zero Timestamp, got zero")
	}
	if loc := rec.Timestamp.Location(); loc != time.UTC {
		t.Errorf("expected UTC Timestamp, got %v", loc)
	}
}

func TestActionTypeConstants(t *testing.T) {
	actions := []ActionType{
		ActionTaskStarted, ActionContextRetrieved, ActionMemoryRetrieved,
		ActionToolCalled, ActionDecisionMade, ActionFileModified, ActionFileChanged,
		ActionTestExecuted, ActionGuardrailTriggered, ActionApprovalRequested,
		ActionChangeAccepted, ActionDeploymentPerformed, ActionProductionOutcome,
		ActionPRCreated, ActionVerificationStarted, ActionVerificationCompleted,
	}
	seen := map[ActionType]bool{}
	for _, a := range actions {
		if a == "" {
			t.Fatal("empty ActionType")
		}
		if seen[a] {
			t.Fatalf("duplicate ActionType: %s", a)
		}
		seen[a] = true
	}
	if len(actions) != 16 {
		t.Fatalf("expected 16 ActionTypes, got %d", len(actions))
	}
}

func TestWhyDecision(t *testing.T) {
	r := New(t.TempDir())
	now := time.Now()
	recs := []Record{
		{AgentID: "a", TaskID: "task1", Action: string(ActionDecisionMade), Timestamp: now.Add(-3 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionDecisionMade), Timestamp: now.Add(-1 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionToolCalled), Timestamp: now},
	}
	for _, rec := range recs {
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record(%+v): %v", rec, err)
		}
	}
	got := r.WhyDecision("task1")
	if len(got) != 2 {
		t.Fatalf("WhyDecision returned %d records, want 2", len(got))
	}
	for _, rec := range got {
		if rec.Action != string(ActionDecisionMade) {
			t.Errorf("WhyDecision returned record with Action=%q, want decision_made", rec.Action)
		}
	}
}

func TestWhatContextUsed(t *testing.T) {
	r := New(t.TempDir())
	now := time.Now()
	recs := []Record{
		{AgentID: "a", TaskID: "task1", Action: string(ActionContextRetrieved), Timestamp: now.Add(-2 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionMemoryRetrieved), Timestamp: now.Add(-1 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionToolCalled), Timestamp: now},
	}
	for _, rec := range recs {
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record(%+v): %v", rec, err)
		}
	}
	got := r.WhatContextUsed("task1")
	if len(got) != 2 {
		t.Fatalf("WhatContextUsed returned %d records, want 2", len(got))
	}
	for _, rec := range got {
		if rec.Action != string(ActionContextRetrieved) && rec.Action != string(ActionMemoryRetrieved) {
			t.Errorf("WhatContextUsed returned record with Action=%q", rec.Action)
		}
	}
}

func TestWhatHappened(t *testing.T) {
	r := New(t.TempDir())
	now := time.Now()
	recs := []Record{
		{AgentID: "a", TaskID: "task1", Action: string(ActionTaskStarted), Timestamp: now.Add(-5 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionToolCalled), Timestamp: now.Add(-4 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionDecisionMade), Timestamp: now.Add(-3 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionFileModified), Timestamp: now.Add(-2 * time.Second)},
		{AgentID: "a", TaskID: "task1", Action: string(ActionTestExecuted), Timestamp: now.Add(-1 * time.Second)},
		{AgentID: "a", TaskID: "other", Action: string(ActionToolCalled), Timestamp: now},
		{AgentID: "a", TaskID: "other", Action: string(ActionDecisionMade), Timestamp: now.Add(-30 * time.Millisecond)},
	}
	for _, rec := range recs {
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record(%+v): %v", rec, err)
		}
	}
	got := r.WhatHappened("task1")
	if len(got) != 5 {
		t.Fatalf("WhatHappened returned %d records, want 5", len(got))
	}
	// Chronological order (oldest first).
	if got[0].Action != string(ActionTaskStarted) {
		t.Errorf("oldest WhatHappened record = %q, want task_started", got[0].Action)
	}
	if got[4].Action != string(ActionTestExecuted) {
		t.Errorf("newest WhatHappened record = %q, want test_executed", got[4].Action)
	}
}

func TestQueryMethodsNilSafe(t *testing.T) {
	var r *Recorder
	if got := r.WhyDecision("task1"); got != nil {
		t.Errorf("nil WhyDecision = %v, want nil", got)
	}
	if got := r.WhatHappened("task1"); got != nil {
		t.Errorf("nil WhatHappened = %v, want nil", got)
	}
}

func TestNewRecord(t *testing.T) {
	r := NewRecord("agent-1", "task-1", ActionToolCalled)
	if r.AgentID != "agent-1" || r.TaskID != "task-1" || r.Action != "tool_called" {
		t.Errorf("NewRecord = %+v", r)
	}
	if r.Timestamp.IsZero() {
		t.Error("NewRecord should set Timestamp")
	}
}

// TestLifecycleSequenceRecordsNewActionTypes verifies the full lifecycle — task
// start, tool call, file change, verification start/complete, PR creation, and
// production outcome — is persisted and retrievable in chronological order,
// including the additive action types.
func TestLifecycleSequenceRecordsNewActionTypes(t *testing.T) {
	r := New(t.TempDir())
	now := time.Now()
	seq := []struct {
		action ActionType
		at     time.Time
	}{
		{ActionTaskStarted, now.Add(-8 * time.Second)},
		{ActionContextRetrieved, now.Add(-7 * time.Second)},
		{ActionToolCalled, now.Add(-6 * time.Second)},
		{ActionDecisionMade, now.Add(-5 * time.Second)},
		{ActionFileChanged, now.Add(-4 * time.Second)},
		{ActionVerificationStarted, now.Add(-3 * time.Second)},
		{ActionTestExecuted, now.Add(-2 * time.Second)},
		{ActionVerificationCompleted, now.Add(-1500 * time.Millisecond)},
		{ActionPRCreated, now.Add(-1 * time.Second)},
		{ActionDeploymentPerformed, now.Add(-500 * time.Millisecond)},
		{ActionProductionOutcome, now},
	}
	for _, s := range seq {
		rec := NewRecord("agentX", "task-lifecycle", s.action)
		rec.Timestamp = s.at
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record(%s): %v", s.action, err)
		}
	}

	all := r.WhatHappened("task-lifecycle")
	if len(all) != len(seq) {
		t.Fatalf("WhatHappened = %d records, want %d", len(all), len(seq))
	}
	want := []ActionType{
		ActionTaskStarted, ActionContextRetrieved, ActionToolCalled, ActionDecisionMade,
		ActionFileChanged, ActionVerificationStarted, ActionTestExecuted,
		ActionVerificationCompleted, ActionPRCreated, ActionDeploymentPerformed,
		ActionProductionOutcome,
	}
	for i, w := range want {
		if all[i].Action != string(w) {
			t.Errorf("record[%d] = %q, want %q", i, all[i].Action, w)
		}
	}

	if got := r.WhichToolsCalled("task-lifecycle"); len(got) != 1 {
		t.Errorf("WhichToolsCalled = %d, want 1", len(got))
	}
	if got := r.WhatVerified("task-lifecycle"); len(got) != 2 {
		t.Errorf("WhatVerified = %d, want 2", len(got))
	}
	if got := r.WhatOutcome("task-lifecycle"); len(got) != 3 {
		t.Errorf("WhatOutcome = %d, want 3 (deploy, outcome, verify-complete)", len(got))
	}
	if got := r.WhatChanged("task-lifecycle"); len(got) != 1 {
		t.Errorf("WhatChanged = %d, want 1 (file_changed)", len(got))
	}
}

func TestTrailText(t *testing.T) {
	r := New(t.TempDir())
	base := time.Now().Add(-10 * time.Minute).UTC()
	for i, rec := range []Record{
		{AgentID: "agentA", TaskID: "task1", Action: "task_started", Context: "fix the N+1", Status: "ok", Timestamp: base},
		{AgentID: "agentA", TaskID: "task1", Action: "tool_called", Arguments: "kern_search n+1", Result: "3 symbols", Status: "ok", Timestamp: base.Add(time.Minute)},
		{AgentID: "agentA", TaskID: "task1", Action: "change_accepted", Status: "ok", Approved: true, Timestamp: base.Add(2 * time.Minute)},
		{AgentID: "agentA", TaskID: "other", Action: "task_started", Status: "ok", Timestamp: base.Add(3 * time.Minute)},
	} {
		if _, err := r.Record(rec); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	// TrailText must be scoped to the task and chronological (not List order).
	got := r.TrailText("task1")
	for _, want := range []string{"flight trail for task task1 (3 records)", "task_started by agentA", "tool_called by agentA", "kern_search n+1", "approved: yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("TrailText missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "other") {
		t.Errorf("TrailText leaked another task's records:\n%s", got)
	}
	if got := r.TrailText("nope"); !strings.Contains(got, "no flight records for task nope") {
		t.Errorf("TrailText unknown task = %q, want no-records message", got)
	}
}

// TestTasksGroupsTrailsByTask pins the task-id to trail linkage: Tasks()
// aggregates every record by TaskID with count, first/last activity, and
// distinct statuses, ordered most recently active first.
func TestTasksGroupsTrailsByTask(t *testing.T) {
	dir := t.TempDir()
	rec := New(dir)
	now := time.Now().UTC()
	recs := []Record{
		{ID: "f-1", AgentID: "a1", TaskID: "t-old", Action: "task_started", Timestamp: now.Add(-2 * time.Hour)},
		{ID: "f-2", AgentID: "a1", TaskID: "t-old", Action: "tool_called", Timestamp: now.Add(-2 * time.Hour).Add(time.Minute), Status: "ok"},
		{ID: "f-3", AgentID: "a2", TaskID: "t-new", Action: "task_started", Timestamp: now.Add(-time.Minute), Status: "ok"},
		{ID: "f-4", AgentID: "a2", TaskID: "", Action: "tool_called", Timestamp: now, Status: "error"},
	}
	for _, r := range recs {
		if _, err := rec.Record(r); err != nil {
			t.Fatal(err)
		}
	}
	sums, err := rec.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 3 {
		t.Fatalf("Tasks() = %d summaries, want 3", len(sums))
	}
	// Newest last-activity first: "" (now), t-new (-1m), t-old (-2h).
	if sums[0].TaskID != "" || sums[1].TaskID != "t-new" || sums[2].TaskID != "t-old" {
		t.Fatalf("Tasks() order = %+v, want [\"\" t-new t-old]", sums)
	}
	old := sums[2]
	if old.Count != 2 || !old.First.Equal(now.Add(-2*time.Hour)) || !old.Last.Equal(now.Add(-2*time.Hour).Add(time.Minute)) {
		t.Fatalf("t-old summary = %+v, want count 2, first -2h, last -2h+1m", old)
	}
	if len(old.Statuses) != 1 || old.Statuses[0] != "ok" {
		t.Fatalf("t-old statuses = %v, want [ok]", old.Statuses)
	}
}

// TestGCRetainsRecentTrailsAndPurgesBuffer pins retention: trails of tasks
// neither among the keepTasks most recently active nor active within
// olderThan are deleted from the store AND the in-memory buffer (so List
// cannot resurrect them), and surviving trails are never truncated.
func TestGCRetainsRecentTrailsAndPurgesBuffer(t *testing.T) {
	dir := t.TempDir()
	rec := New(dir)
	now := time.Now().UTC()
	for i, task := range []string{"t-old", "t-mid", "t-new"} {
		// t-old is the oldest (-2h), t-new the newest (now) — the timestamps
		// make t-new the most recently active task.
		if _, err := rec.Record(Record{ID: fmt.Sprintf("f-%d-1", i), TaskID: task, Timestamp: now.Add(-time.Duration(2-i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := rec.Record(Record{ID: fmt.Sprintf("f-%d-2", i), TaskID: task, Timestamp: now.Add(-time.Duration(2-i) * time.Hour).Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	// Keep only the 1 most recently active task (t-new): t-old and t-mid
	// trails must be deleted (4 records), the t-new trail must survive whole.
	deleted, err := rec.GC(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 4 {
		t.Fatalf("GC deleted %d records, want 4", deleted)
	}
	recs, err := rec.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("List() after GC = %d records, want 2 (t-new trail intact, buffer not resurrecting)", len(recs))
	}
	for _, r := range recs {
		if r.TaskID != "t-new" {
			t.Fatalf("List() after GC contains task %q, want only t-new", r.TaskID)
		}
	}
	// Age gate: a fresh store, keep only tasks active within the last 90
	// minutes -> t-mid (-1h) survives, t-old (-2h) does not.
	dir2 := t.TempDir()
	rec2 := New(dir2)
	if _, err := rec2.Record(Record{ID: "g-1", TaskID: "t-old", Timestamp: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec2.Record(Record{ID: "g-2", TaskID: "t-mid", Timestamp: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	deleted, err = rec2.GC(0, 90*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("age-gated GC deleted %d records, want 1", deleted)
	}
	recs, err = rec2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].TaskID != "t-mid" {
		t.Fatalf("age-gated List() = %+v, want only t-mid", recs)
	}
}
