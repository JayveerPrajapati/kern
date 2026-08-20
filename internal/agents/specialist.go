package agents

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Specialist is a configured agent instance for a role, pairing a runtime
// [agent.Agent] identity with its role and capabilities.
type Specialist struct {
	Agent        agent.Agent
	Role         Role
	Capabilities []string // what this specialist can do
}

// NewSpecialist creates a specialist. The runtime agent uses the name as its
// ID and display name, and its type as the role string.
func NewSpecialist(role Role, name string) *Specialist {
	return &Specialist{
		Agent: agent.Agent{
			Agent: domain.Agent{
				ID:        name,
				Name:      name,
				Type:      string(role),
				CreatedAt: time.Now(),
			},
		},
		Role: role,
	}
}

// SpecialistRegistry is an in-memory registry of specialists keyed by name.
type SpecialistRegistry struct {
	mu          sync.RWMutex
	specialists map[string]*Specialist // name -> specialist
}

// NewSpecialistRegistry creates an empty specialist registry.
func NewSpecialistRegistry() *SpecialistRegistry {
	return &SpecialistRegistry{specialists: map[string]*Specialist{}}
}

// Register adds a specialist. It fails closed on a nil specialist, an empty
// name, or a duplicate name.
func (r *SpecialistRegistry) Register(s *Specialist) error {
	if s == nil || s.Agent.ID == "" {
		return fmt.Errorf("agents: cannot register specialist with empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specialists[s.Agent.ID]; exists {
		return fmt.Errorf("agents: specialist %q already registered", s.Agent.ID)
	}
	r.specialists[s.Agent.ID] = s
	return nil
}

// Get returns a specialist by name. It reports whether the specialist was
// found.
func (r *SpecialistRegistry) Get(name string) (*Specialist, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specialists[name]
	return s, ok
}

// ByRole returns all specialists for a role, sorted by name. Empty when none.
func (r *SpecialistRegistry) ByRole(role Role) []*Specialist {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Specialist
	for _, s := range r.specialists {
		if s.Role == role {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent.ID < out[j].Agent.ID })
	return out
}
