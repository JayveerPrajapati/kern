// Package identity provides agent identities and the permission model used
// by the governance change firewall.
package governance

import (
	"fmt"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Permission represents a single permission grant, pairing a Resource (what is
// acted on) with an Action (what is done). Permission enforcement is
// fail-closed: an agent cannot perform any action unless explicitly granted, so
// the absence of a permission is a denial. Approval requirements are
// policy-level (in DefaultPolicies / WithPolicies), not per-agent.
type Permission struct {
	Resource string // "source", "docs", "tests", "production", "database", etc.
	Action   string // "read", "write", "execute", "deploy", "drop"
}

// AgentIdentity extends domain.Agent with permission enforcement.
type AgentIdentity struct {
	domain.Agent
	Permissions []Permission
}

// NewAgent creates a new agent identity with the given permissions. The
// CreatedAt timestamp is set to the current time.
func NewAgent(id, name, agentType string, perms []Permission) *AgentIdentity {
	return &AgentIdentity{
		Agent: domain.Agent{
			ID:        id,
			Name:      name,
			Type:      agentType,
			CreatedAt: time.Now(),
		},
		Permissions: perms,
	}
}

// Can reports whether the agent has the given permission grant. Matching is
// exact on both resource and action.
func (a *AgentIdentity) Can(resource, action string) bool {
	for _, p := range a.Permissions {
		if p.Resource == resource && p.Action == action {
			return true
		}
	}
	return false
}

// HasPermission is an alias for Can, exposed for readability at the firewall
// call sites.
func (a *AgentIdentity) HasPermission(resource, action string) bool {
	return a.Can(resource, action)
}

// agentRegistry is the in-memory agent registry; no persistence is needed.
// agentRegistryMu guards the registry: Enterprise Server registers agents
// concurrently with MCP GetAgent reads on the per-call critical path.
var (
	agentRegistryMu sync.RWMutex
	agentRegistry   = map[string]*AgentIdentity{}
)

// RegisterAgent stores an agent identity for later lookup by ID. It returns an
// error when given a nil agent or an agent with an empty ID.
func RegisterAgent(a *AgentIdentity) error {
	if a == nil || a.ID == "" {
		return fmt.Errorf("governance: cannot register nil or empty-ID agent")
	}
	agentRegistryMu.Lock()
	defer agentRegistryMu.Unlock()
	agentRegistry[a.ID] = a
	return nil
}

// GetAgent retrieves a registered agent by ID. It returns an error (fail
// closed) when the agent is unknown.
func GetAgent(id string) (*AgentIdentity, error) {
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	a, ok := agentRegistry[id]
	if !ok {
		return nil, fmt.Errorf("governance: agent %q not found", id)
	}
	return a, nil
}
