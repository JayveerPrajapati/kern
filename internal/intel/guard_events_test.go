package intel

import (
	"fmt"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestGuardEventShapes(t *testing.T) {
	v := Violation{Symbol: "DoWork", CallerFile: "web/caller.go", RuleFrom: "web", RuleTo: "db"}
	e := GuardEvent(v)
	if e.ID == "" || e.Kind != eventbus.ArchitectureViolation || e.Source != "guard" || e.Subject != "DoWork" || e.AgentID != "guard" {
		t.Fatalf("violation event shape wrong: %+v", e)
	}
	if got := fmt.Sprint(e.Payload); got != "web/caller.go -> DoWork forbidden by rule web -> db" {
		t.Fatalf("payload wrong: %q", got)
	}
	// Subject falls back to the caller file when the symbol is empty.
	v2 := Violation{CallerFile: "web/caller.go", RuleFrom: "web", RuleTo: "db"}
	if e2 := GuardEvent(v2); e2.Subject != "web/caller.go" {
		t.Fatalf("subject fallback wrong: %+v", e2)
	}
	w := GuardNotConfiguredEvent()
	if w.ID == "" || w.ID == e.ID || w.Kind != eventbus.ArchitectureWarning || w.Source != "guard" || w.AgentID != "guard" {
		t.Fatalf("warning event shape wrong: %+v", w)
	}
	if got := fmt.Sprint(w.Payload); got != "boundaries not configured — architecture guard NOT enforced" {
		t.Fatalf("warning payload wrong: %q", got)
	}
}

func TestGuardEventsBatch(t *testing.T) {
	vs := []Violation{
		{Symbol: "A", CallerFile: "f.go", RuleFrom: "x", RuleTo: "y"},
		{Symbol: "B", CallerFile: "g.go", RuleFrom: "x", RuleTo: "z"},
	}
	got := GuardEvents(vs, true)
	if len(got) != 3 {
		t.Fatalf("expected 2 violations + 1 warning, got %d", len(got))
	}
	if got[0].Subject != "A" || got[1].Subject != "B" || got[2].Kind != eventbus.ArchitectureWarning {
		t.Fatalf("batch order wrong: %+v", got)
	}
	// IDs are unique and non-empty.
	seen := map[string]bool{}
	for _, e := range got {
		if e.ID == "" || seen[e.ID] {
			t.Fatalf("event IDs must be unique and non-empty: %+v", e)
		}
		seen[e.ID] = true
	}
	if out := GuardEvents(nil, false); len(out) != 0 {
		t.Fatalf("empty batch expected, got %+v", out)
	}
}
