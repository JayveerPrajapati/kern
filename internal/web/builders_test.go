package web

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
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
