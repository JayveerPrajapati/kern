package web

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestBuildIncidentsEmptyStore pins the empty-store path: no incidents and no
// error.
func TestBuildIncidentsEmptyStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := &App{inter: incident.NewStore(t.TempDir())}

	got, err := a.buildIncidents()
	if err != nil {
		t.Fatalf("buildIncidents(empty) error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("buildIncidents(empty) = %#v, want empty slice", got)
	}
}

// TestBuildIncidentsMapsFields pins the field mapping and ordering: summaries
// carry ID/Title/Severity/Status/AffectedService/UpdatedAt, in the order the
// store returns them (newest first).
func TestBuildIncidentsMapsFields(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	store := incident.NewStore(t.TempDir())
	a := &App{inter: store}

	seeds := []*domain.Incident{
		{ID: "inc-1", Title: "checkout 500s", Severity: domain.SeverityError, Status: domain.IncidentOpen, AffectedService: "checkout"},
		{ID: "inc-2", Title: "payments latency", Severity: domain.SeverityWarning, Status: domain.IncidentRootCauseFound, AffectedService: "payments"},
	}
	for _, inc := range seeds {
		if _, err := store.Save(inc); err != nil {
			t.Fatalf("store.Save(%s): %v", inc.ID, err)
		}
	}

	got, err := a.buildIncidents()
	if err != nil {
		t.Fatalf("buildIncidents error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("buildIncidents = %d summaries, want 2", len(got))
	}
	// The store lists newest first; buildIncidents preserves that order.
	first, second := got[0], got[1]
	if first.ID != "inc-2" || second.ID != "inc-1" {
		t.Fatalf("buildIncidents order = [%s, %s], want [inc-2, inc-1] (store order)", first.ID, second.ID)
	}
	if first.Title != "payments latency" || first.Severity != "warning" || first.Status != "ROOT_CAUSE_FOUND" || first.AffectedService != "payments" {
		t.Errorf("first summary = %+v, want payments latency/warning/ROOT_CAUSE_FOUND/payments", first)
	}
	if second.Title != "checkout 500s" || second.Severity != "error" || second.Status != "OPEN" || second.AffectedService != "checkout" {
		t.Errorf("second summary = %+v, want checkout 500s/error/OPEN/checkout", second)
	}
	if first.UpdatedAt.IsZero() || second.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt not stamped: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

// TestBuildMemoryEmpty pins the empty-store path for the memory builder.
func TestBuildMemoryEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := &App{memories: memory.NewMemoryStore(t.TempDir())}
	got := a.buildMemory()
	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("buildMemory(empty) = %#v, want empty slice", got)
	}
}

// TestBuildMemoryMapsFields pins the memory field mapping (ID/Type/Content/
// Source/Scope/Tags/Status/CreatedAt/UpdatedAt) for a stored memory.
func TestBuildMemoryMapsFields(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	store := memory.NewMemoryStore(t.TempDir())
	base := time.Now().UTC().Add(-time.Hour)
	if _, err := store.Add(domain.Memory{ID: "m1", Type: "lesson", Content: "the fix", Source: "session", Scope: "repo", Tags: []string{"a"}, CreatedAt: base, UpdatedAt: base}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	a := &App{memories: store}
	got := a.buildMemory()
	if len(got.Items) != 1 {
		t.Fatalf("buildMemory = %d items, want 1", len(got.Items))
	}
	m := got.Items[0]
	if m.ID != "m1" || m.Type != "lesson" || m.Content != "the fix" || m.Source != "session" || m.Scope != "repo" || len(m.Tags) != 1 || m.Tags[0] != "a" || m.Status != "current" {
		t.Errorf("memory mapping mismatch: %+v", m)
	}
}

// TestBuildTasksEmpty pins the empty-task path for the efficiency table.
func TestBuildTasksEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := agent.NewRegistry()
	a := &App{tasks: reg}
	got, err := a.buildTasks()
	if err != nil {
		t.Fatalf("buildTasks(empty) error: %v", err)
	}
	if got == nil || len(got.Tasks) != 0 {
		t.Fatalf("buildTasks(empty) = %#v, want empty tasks", got)
	}
}

// TestBuildApprovalsNilGates pins the nil-guard: an App without approval
// stores returns an empty list instead of panicking.
func TestBuildApprovalsNilGates(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := &App{}
	got := a.buildApprovals()
	if got == nil || len(got) != 0 {
		t.Fatalf("buildApprovals(nil gates) = %#v, want empty", got)
	}
}
