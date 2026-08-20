package agent

import "testing"

func TestHandoffManagerHandoff(t *testing.T) {
	h := NewHandoffManager()
	rec := h.Handoff("t1", "a1", "a2", "plan -> code")
	if rec.TaskID != "t1" || rec.FromAgent != "a1" || rec.ToAgent != "a2" || rec.Reason != "plan -> code" {
		t.Fatalf("Handoff record = %+v", rec)
	}
	if rec.Timestamp.IsZero() {
		t.Fatal("Handoff: zero timestamp")
	}
}

func TestHandoffHistoryOrder(t *testing.T) {
	h := NewHandoffManager()
	h.Handoff("t1", "a1", "a2", "r1")
	h.Handoff("t1", "a2", "a3", "r2")
	h.Handoff("t1", "a3", "a4", "r3")
	hist := h.History("t1")
	if len(hist) != 3 {
		t.Fatalf("History len = %d, want 3", len(hist))
	}
	// Oldest first.
	if hist[0].ToAgent != "a2" || hist[2].ToAgent != "a4" {
		t.Fatalf("History order = %s,%s,%s, want a2,...,a4", hist[0].ToAgent, hist[1].ToAgent, hist[2].ToAgent)
	}
	// Other tasks unaffected.
	if len(h.History("t2")) != 0 {
		t.Fatal("History(t2) should be empty")
	}
}

func TestHandoffLast(t *testing.T) {
	h := NewHandoffManager()
	if _, ok := h.Last("t1"); ok {
		t.Fatal("Last on empty task: want not found")
	}
	h.Handoff("t1", "a1", "a2", "r1")
	h.Handoff("t1", "a2", "a3", "r2")
	last, ok := h.Last("t1")
	if !ok {
		t.Fatal("Last: not found")
	}
	if last.ToAgent != "a3" || last.Reason != "r2" {
		t.Fatalf("Last = %+v, want ToAgent a3", last)
	}
}
