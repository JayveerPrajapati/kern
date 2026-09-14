package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

func TestAuditTrailRecordsAndAssignsFields(t *testing.T) {
	trail := NewAuditTrail()
	if err := trail.Record(AuditEvent{AgentID: "agent-1", Operation: OpAdd, Allowed: true}); err != nil {
		t.Fatal(err)
	}
	evs := trail.Recent(0)
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	ev := evs[0]
	if ev.ID == "" {
		t.Fatal("Record must assign an ID")
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("Record must assign a timestamp")
	}
	if ev.AgentID != "agent-1" || ev.Operation != OpAdd {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if !ev.Allowed {
		t.Fatal("Allowed field must pass through")
	}
}

func TestAuditTrailRecentNewestFirst(t *testing.T) {
	trail := NewAuditTrail()
	for i := 0; i < 5; i++ {
		if err := trail.Record(AuditEvent{AgentID: "a", Operation: OpAdd}); err != nil {
			t.Fatal(err)
		}
	}
	recent := trail.Recent(2)
	if len(recent) != 2 {
		t.Fatalf("got %d, want 2", len(recent))
	}
	if recent[0].ID != "memory-audit-5" || recent[1].ID != "memory-audit-4" {
		t.Fatalf("recent order wrong: %s, %s", recent[0].ID, recent[1].ID)
	}
}

func TestAuditTrailFilter(t *testing.T) {
	trail := NewAuditTrail()
	if err := trail.Record(AuditEvent{AgentID: "agent-1", Operation: OpAdd}); err != nil {
		t.Fatal(err)
	}
	if err := trail.Record(AuditEvent{AgentID: "agent-2", Operation: OpRecall}); err != nil {
		t.Fatal(err)
	}
	if err := trail.Record(AuditEvent{AgentID: "agent-1", Operation: OpDelete}); err != nil {
		t.Fatal(err)
	}

	if got := trail.FilterByAgent("agent-1"); len(got) != 2 {
		t.Fatalf("agent-1 events: got %d, want 2", len(got))
	}
	if got := trail.FilterByOperation(OpRecall); len(got) != 1 || got[0].AgentID != "agent-2" {
		t.Fatalf("recall events: got %+v", got)
	}
	if got := trail.FilterByOperation(OpAdd); len(got) != 1 {
		t.Fatalf("add events: got %d, want 1", len(got))
	}
}

func TestAuditTrailPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()

	trail1 := NewAuditTrail().WithDir(dir)
	if err := trail1.Record(AuditEvent{AgentID: "agent-1", Operation: OpAdd, MemoryID: "m1"}); err != nil {
		t.Fatal(err)
	}
	if err := trail1.Record(AuditEvent{AgentID: "agent-2", Operation: OpDelete, MemoryID: "m2"}); err != nil {
		t.Fatal(err)
	}

	// A fresh trail over the same directory replays persisted events.
	trail2 := NewAuditTrail().WithDir(dir)
	if got := trail2.Count(); got != 2 {
		t.Fatalf("replayed count: got %d, want 2", got)
	}
	evs := trail2.Recent(0)
	// Write order must be restored (numeric seq), not lexical key order.
	if evs[0].MemoryID != "m2" || evs[1].MemoryID != "m1" {
		t.Fatalf("replay order wrong: %+v", evs)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory-audit-1.json")); err != nil {
		t.Fatalf("expected persisted event file: %v", err)
	}
}

func TestAuditTrailInMemoryCap(t *testing.T) {
	trail := NewAuditTrail()
	for i := 0; i < maxAuditEvents+50; i++ {
		if err := trail.Record(AuditEvent{AgentID: "a", Operation: OpAdd}); err != nil {
			t.Fatal(err)
		}
	}
	if got := trail.Count(); got != maxAuditEvents {
		t.Fatalf("in-memory cap: got %d, want %d", got, maxAuditEvents)
	}
}

func TestGovernedStoreRecordsAuditEvents(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	gov := &Governance{
		Access:    NewAccessControl(DefaultPolicy()),
		Audit:     NewAuditTrail().WithDir(t.TempDir()),
		Retention: DefaultRetention(),
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)

	m, err := s.Add(domain.Memory{Content: "remember this", Type: domain.MemoryLesson, Source: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizedRecall(Query{Text: "remember"}, "agent-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}

	if got := gov.Audit.FilterByAgent("agent-1"); len(got) != 2 {
		t.Fatalf("agent-1 events: got %d, want 2 (add+recall)", len(got))
	}
	if got := gov.Audit.FilterByOperation(OpAdd); len(got) != 1 || got[0].MemoryID != m.ID {
		t.Fatalf("add audit: got %+v", got)
	}
	if got := gov.Audit.FilterByOperation(OpDelete); len(got) != 1 || !got[0].Allowed {
		t.Fatalf("delete audit: got %+v", got)
	}
	if got := gov.Audit.FilterByOperation(OpRecall); len(got) != 1 || got[0].AgentID != "agent-1" {
		t.Fatalf("recall audit: got %+v", got)
	}
	// Every event carries a timestamp.
	for _, ev := range gov.Audit.Recent(0) {
		if ev.Timestamp.IsZero() {
			t.Fatalf("event %s has no timestamp", ev.ID)
		}
	}
}

func TestAuditTrailPersistenceDoesNotFailStoreOps(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// A store that fails on every Put must not break memory operations.
	gov := &Governance{
		Access: NewAccessControl(DefaultPolicy()),
		Audit:  NewAuditTrail().WithStore(&failingStore{}),
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)
	if _, err := s.Add(domain.Memory{Content: "still works", Type: domain.MemoryLesson}); err != nil {
		t.Fatalf("Add failed when audit persistence fails: %v", err)
	}
}

// failingStore is a storage.Store whose Put always fails, for testing the
// best-effort audit contract.
type failingStore struct{}

func (f *failingStore) Put(_ context.Context, _ string, _ json.RawMessage) error {
	return errAuditStore
}
func (f *failingStore) Get(_ context.Context, _ string) (json.RawMessage, error) {
	return nil, errAuditStore
}
func (f *failingStore) Delete(_ context.Context, _ string) error { return errAuditStore }
func (f *failingStore) List(_ context.Context) ([]storage.Entry, error) {
	return nil, errAuditStore
}

var errAuditStore = os.ErrPermission
