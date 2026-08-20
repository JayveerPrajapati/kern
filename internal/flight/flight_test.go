package flight

import (
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
		ActionToolCalled, ActionDecisionMade, ActionFileModified,
		ActionTestExecuted, ActionGuardrailTriggered, ActionApprovalRequested,
		ActionChangeAccepted, ActionDeploymentPerformed, ActionProductionOutcome,
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
	if len(actions) != 12 {
		t.Fatalf("expected 12 ActionTypes, got %d", len(actions))
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
