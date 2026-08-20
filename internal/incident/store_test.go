package incident

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestIncidentStoreSaveListGet(t *testing.T) {
	st := NewStore(t.TempDir())

	inc := &domain.Incident{ID: "inc-1", Title: "checkout 500s", Severity: domain.SeverityError, Status: domain.IncidentOpen, AffectedService: "checkout"}
	if _, err := st.Save(inc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := st.Save(&domain.Incident{ID: "inc-2", Title: "payments latency", Status: domain.IncidentPRCreated, AffectedService: "payments"}); err != nil {
		t.Fatalf("Save inc-2: %v", err)
	}

	list, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	// Newest first (inc-2 saved after inc-1).
	if list[0].ID != "inc-2" {
		t.Fatalf("first = %q, want inc-2", list[0].ID)
	}

	got, err := st.Get("inc-1")
	if err != nil {
		t.Fatalf("Get inc-1: %v", err)
	}
	if got.AffectedService != "checkout" {
		t.Fatalf("got.AffectedService = %q", got.AffectedService)
	}

	if _, err := st.Get("nope"); err == nil {
		t.Fatal("expected os.ErrNotExist for unknown id")
	}
}

func TestIncidentStoreReplaceKeepsCount(t *testing.T) {
	st := NewStore(t.TempDir())
	for _, id := range []string{"a", "b", "c"} {
		if _, err := st.Save(&domain.Incident{ID: id, Status: domain.IncidentOpen}); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}
	// Replace "b" (update status) — count stays 3.
	if _, err := st.Save(&domain.Incident{ID: "b", Status: domain.IncidentFixVerified}); err != nil {
		t.Fatalf("Save b again: %v", err)
	}
	list, _ := st.List()
	if len(list) != 3 {
		t.Fatalf("list length = %d, want 3", len(list))
	}
	if got, _ := st.Get("b"); got.Status != domain.IncidentFixVerified {
		t.Fatalf("b status = %q", got.Status)
	}
}
