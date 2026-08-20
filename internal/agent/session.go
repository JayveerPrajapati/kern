package agent

import (
	"fmt"
	"sync"
	"time"
)

// Session tracks an agent's working context across tasks.
type Session struct {
	ID          string
	AgentID     string
	ProjectRoot string
	StartedAt   time.Time
	LastActive  time.Time
	TaskIDs     []string
}

// sessionSeq is the package-level counter for deterministic session IDs
// ("s-<n>").
var sessionSeq struct {
	sync.Mutex
	n int
}

func nextSessionID() string {
	sessionSeq.Lock()
	defer sessionSeq.Unlock()
	sessionSeq.n++
	return fmt.Sprintf("s-%d", sessionSeq.n)
}

// SessionStore manages agent sessions in memory.
type SessionStore struct {
	sessions map[string]Session // sessionID -> session
}

// NewSessionStore creates a session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: map[string]Session{}}
}

// Create starts a new session for an agent and returns it.
func (s *SessionStore) Create(agentID, projectRoot string) Session {
	now := time.Now()
	sess := Session{
		ID:          nextSessionID(),
		AgentID:     agentID,
		ProjectRoot: projectRoot,
		StartedAt:   now,
		LastActive:  now,
		TaskIDs:     []string{},
	}
	s.sessions[sess.ID] = sess
	return sess
}

// Get retrieves a session. It reports whether the session was found.
func (s *SessionStore) Get(sessionID string) (Session, bool) {
	sess, ok := s.sessions[sessionID]
	return sess, ok
}

// Touch updates the LastActive timestamp of a session. It is a no-op for
// unknown session IDs.
func (s *SessionStore) Touch(sessionID string) {
	sess, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	sess.LastActive = time.Now()
	s.sessions[sessionID] = sess
}

// ForAgent lists sessions for an agent, newest first (by LastActive, then
// StartedAt).
func (s *SessionStore) ForAgent(agentID string) []Session {
	var out []Session
	for _, sess := range s.sessions {
		if sess.AgentID == agentID {
			out = append(out, sess)
		}
	}
	// Newest first: sort by LastActive descending; ties by StartedAt descending.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && newer(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// newer reports whether a was active more recently than b.
func newer(a, b Session) bool {
	if a.LastActive.Equal(b.LastActive) {
		return a.StartedAt.After(b.StartedAt)
	}
	return a.LastActive.After(b.LastActive)
}
