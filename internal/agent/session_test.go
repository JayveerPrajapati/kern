package agent

import "testing"

func TestSessionCreate(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("agent-1", "/repo")
	if sess.ID == "" {
		t.Fatal("Create: empty ID")
	}
	if sess.AgentID != "agent-1" || sess.ProjectRoot != "/repo" {
		t.Fatalf("Create session = %+v", sess)
	}
	if sess.StartedAt.IsZero() || sess.LastActive.IsZero() {
		t.Fatal("Create: zero timestamps")
	}
}

func TestSessionGet(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("agent-1", "/repo")
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("Get AgentID = %q", got.AgentID)
	}
	if _, ok := s.Get("s-9999"); ok {
		t.Fatal("Get(unknown): found; want not found")
	}
}

func TestSessionTouch(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("agent-1", "/repo")
	s.Touch(sess.ID)
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("Get after Touch: not found")
	}
	if got.LastActive.Before(sess.LastActive) {
		t.Fatal("Touch did not advance LastActive")
	}
	// Unknown session is a safe no-op.
	s.Touch("s-9999")
}

func TestSessionForAgentOrdering(t *testing.T) {
	s := NewSessionStore()
	first := s.Create("agent-1", "/a")
	second := s.Create("agent-1", "/b")
	_ = s.Create("agent-2", "/other") // different agent, must be excluded

	// Touch the first so it is the most recently active.
	s.Touch(first.ID)

	list := s.ForAgent("agent-1")
	if len(list) != 2 {
		t.Fatalf("ForAgent len = %d, want 2", len(list))
	}
	if list[0].ID != first.ID {
		t.Fatalf("ForAgent[0] = %s, want the touched (newest) session %s", list[0].ID, first.ID)
	}
	if list[1].ID != second.ID {
		t.Fatalf("ForAgent[1] = %s, want %s", list[1].ID, second.ID)
	}
}

func TestSessionTaskIDs(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("agent-1", "/repo")
	if sess.TaskIDs == nil {
		t.Fatal("Create: TaskIDs should be initialized (non-nil slice)")
	}
}
