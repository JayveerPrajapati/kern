// Package identity provides agent identities and the permission model used
// by the governance change firewall.

package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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

// ListAgents returns every registered agent, sorted by ID.
func ListAgents() []*AgentIdentity {
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	ids := make([]string, 0, len(agentRegistry))
	for id := range agentRegistry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*AgentIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, agentRegistry[id])
	}
	return out
}

// PersistedAgents returns the agents stored at <root>/.kern/agents.json
// without touching the in-memory registry. Used by listing surfaces that must
// show only identities that survive across processes (kern org agents), not
// incidental in-memory registrations (e.g. the built-in default agent).
func PersistedAgents(root string) []*AgentIdentity {
	data, err := os.ReadFile(agentStorePath(root))
	if err != nil {
		return nil
	}
	var agents []*AgentIdentity
	if err := json.Unmarshal(data, &agents); err != nil {
		return nil
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// agentStorePath returns the per-project agent identity store. The store
// lives under the project's .kern directory, next to the other governance
// stores (approvals.json, audit/), and is gitignored by the standard
// "kern generated" section.
func agentStorePath(root string) string {
	return filepath.Join(root, ".kern", "agents.json")
}

// PersistAgent upserts a single agent into the project store at
// <root>/.kern/agents.json, leaving every other stored agent intact. This is
// the registration surface for `kern org agents register`: it writes exactly
// the explicitly-registered identity, never incidental in-memory
// registrations like the built-in default agent.
func PersistAgent(root string, a *AgentIdentity) error {
	if a == nil || a.ID == "" {
		return fmt.Errorf("governance: cannot persist nil or empty-ID agent")
	}
	path := agentStorePath(root)
	var agents []*AgentIdentity
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &agents)
	}
	replaced := false
	for i := range agents {
		if agents[i].ID == a.ID {
			agents[i] = a
			replaced = true
			break
		}
	}
	if !replaced {
		agents = append(agents, a)
	}
	data, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return fmt.Errorf("governance: encode agents: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("governance: create agent store dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("governance: write agent store: %w", err)
	}
	return nil
}

// SaveAgents persists the current in-memory registry to the project store at
// <root>/.kern/agents.json. It is the persistence half of agent identity:
// CLI processes are short-lived, so a registration made by `kern org agents
// register` must survive the process that created it. A write failure is
// surfaced (fail loud), never silently swallowed: an agent that exists only
// in memory is exactly the "unknown agent" class of denial this store
// prevents.
func SaveAgents(root string) error {
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	agents := make([]*AgentIdentity, 0, len(agentRegistry))
	for _, a := range agentRegistry {
		agents = append(agents, a)
	}
	data, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return fmt.Errorf("governance: encode agents: %w", err)
	}
	path := agentStorePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("governance: create agent store dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("governance: write agent store: %w", err)
	}
	return nil
}

// LoadAgents merges the persisted agents from <root>/.kern/agents.json into
// the in-memory registry. Best-effort by design: a missing store (fresh
// project) or an unreadable/corrupt store leaves the registry unchanged —
// lookups then fail closed with "unknown agent", which is the safe default.
func LoadAgents(root string) error {
	path := agentStorePath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("governance: read agent store: %w", err)
	}
	var agents []*AgentIdentity
	if err := json.Unmarshal(data, &agents); err != nil {
		return fmt.Errorf("governance: decode agent store: %w", err)
	}
	agentRegistryMu.Lock()
	defer agentRegistryMu.Unlock()
	for _, a := range agents {
		if a != nil && a.ID != "" {
			agentRegistry[a.ID] = a
		}
	}
	return nil
}
