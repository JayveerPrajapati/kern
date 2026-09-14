// Memory governance: access control for memory operations (P1-007).
//
// A Governance layer bundles three concerns:
//   - AccessControl: who may read, write and delete memories (per-agent
//     permissions, resolved from a Policy).
//   - AuditTrail: a persistent record of every memory operation (add, recall,
//     delete, ...) with timestamp, agent and outcome.
//   - RetentionPolicy: memory lifecycle — auto-expiry, archiving to the
//     historical state, and a hard size cap.
//
// Governance is opt-in: a MemoryStore created with NewMemoryStore has no
// governance attached and behaves exactly as before. Attach one with
// WithGovernance (or the environment-driven WithEnvGovernance) to enforce
// permissions and record audits. DefaultPolicy is fully permissive, so
// attaching governance never breaks existing behavior — it only starts
// recording the audit trail. Tighten the policy to actually restrict.

package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrPermissionDenied is returned when a memory operation is denied by the
// access-control policy. Match it with errors.Is: the concrete error is a
// *PermissionError which also satisfies Is.
var ErrPermissionDenied = errors.New("memory: permission denied")

// Permission is a memory operation a caller may be granted.
type Permission string

const (
	// PermissionRead grants recalling/listing memories.
	PermissionRead Permission = "read"
	// PermissionWrite grants adding, updating and superseding memories.
	PermissionWrite Permission = "write"
	// PermissionDelete grants removing memories.
	PermissionDelete Permission = "delete"
)

// PermissionError describes a denied access-control decision.
type PermissionError struct {
	Agent      string
	Permission Permission
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("memory: %s denied %s permission", e.Agent, e.Permission)
}

// Is lets errors.Is(err, ErrPermissionDenied) match a *PermissionError.
func (e *PermissionError) Is(target error) bool { return target == ErrPermissionDenied }

// Policy is the memory access-control policy.
//
//   - Agents maps an agent ID to the permissions it holds. An agent not listed
//     falls back to DefaultPermissions.
//   - DefaultPermissions applies to agents not listed in Agents.
//   - AllowedAgents, when non-empty, is an allowlist: only these agents may
//     access memory at all. Combined with DefaultPermissions it sets the
//     baseline; per-agent grants in Agents can only tighten or extend within
//     the allowlist.
//   - DeniedAgents always wins: an agent listed here is denied every
//     permission regardless of other grants.
//
// An empty Permissions list for an agent means "no permissions".
type Policy struct {
	Agents             map[string][]Permission `json:"agents,omitempty"`
	DefaultPermissions []Permission            `json:"default_permissions,omitempty"`
	AllowedAgents      []string                `json:"allowed_agents,omitempty"`
	DeniedAgents       []string                `json:"denied_agents,omitempty"`
}

// DefaultPolicy returns a fully permissive policy: every agent may read,
// write and delete. This matches the legacy (un-governed) behavior, so it is
// the safe baseline — tighten it to actually govern.
func DefaultPolicy() Policy {
	return Policy{
		DefaultPermissions: []Permission{PermissionRead, PermissionWrite, PermissionDelete},
	}
}

// has reports whether perms contains perm.
func has(perms []Permission, perm Permission) bool {
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// PolicyFromEnv loads a policy from KERN_MEMORY_POLICY (inline JSON) or, when
// that is empty, KERN_MEMORY_POLICY_FILE (path to a JSON policy file). When
// neither is set it returns DefaultPolicy with no error. Malformed input is
// returned as an error so callers can decide whether to fail or fall back.
func PolicyFromEnv() (Policy, error) {
	if raw := os.Getenv("KERN_MEMORY_POLICY"); raw != "" {
		p, err := parsePolicyJSON([]byte(raw))
		if err != nil {
			return Policy{}, fmt.Errorf("memory: KERN_MEMORY_POLICY: %w", err)
		}
		return p, nil
	}
	if path := os.Getenv("KERN_MEMORY_POLICY_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Policy{}, fmt.Errorf("memory: KERN_MEMORY_POLICY_FILE: %w", err)
		}
		p, err := parsePolicyJSON(b)
		if err != nil {
			return Policy{}, fmt.Errorf("memory: policy file %s: %w", path, err)
		}
		return p, nil
	}
	return DefaultPolicy(), nil
}

func parsePolicyJSON(b []byte) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// AccessControl enforces a Policy. It is immutable: build once, query often.
type AccessControl struct {
	policy Policy
}

// NewAccessControl builds an access-control enforcer from a policy.
func NewAccessControl(p Policy) *AccessControl { return &AccessControl{policy: p} }

// Policy returns the policy being enforced.
func (a *AccessControl) Policy() Policy { return a.policy }

// Can reports whether agentID holds perm under the policy.
func (a *AccessControl) Can(agentID string, perm Permission) bool {
	p := a.policy
	if slices.Contains(p.DeniedAgents, agentID) {
		return false
	}
	if len(p.AllowedAgents) > 0 && !slices.Contains(p.AllowedAgents, agentID) {
		return false
	}
	if perms, ok := p.Agents[agentID]; ok {
		return has(perms, perm)
	}
	return has(p.DefaultPermissions, perm)
}

// CanRead reports whether agentID may read memories.
func (a *AccessControl) CanRead(agentID string) bool { return a.Can(agentID, PermissionRead) }

// CanWrite reports whether agentID may write memories.
func (a *AccessControl) CanWrite(agentID string) bool { return a.Can(agentID, PermissionWrite) }

// CanDelete reports whether agentID may delete memories.
func (a *AccessControl) CanDelete(agentID string) bool { return a.Can(agentID, PermissionDelete) }

// Authorize returns nil when agentID holds perm, or a *PermissionError
// wrapping ErrPermissionDenied otherwise.
func (a *AccessControl) Authorize(agentID string, perm Permission) error {
	if a.Can(agentID, perm) {
		return nil
	}
	return &PermissionError{Agent: agentID, Permission: perm}
}

// Governance bundles memory access control, the audit trail, and retention
// policies for a MemoryStore.
type Governance struct {
	Access    *AccessControl
	Audit     *AuditTrail
	Retention RetentionPolicy
}

// Enabled reports whether any governance concern is configured.
func (g *Governance) Enabled() bool {
	return g != nil && (g.Access != nil || g.Audit != nil || g.Retention.Enabled)
}

// NewGovernance assembles a governance layer for a project root from the
// environment:
//
//	KERN_MEMORY_POLICY / KERN_MEMORY_POLICY_FILE  access-control policy (JSON)
//	KERN_MEMORY_RETENTION / KERN_MEMORY_RETENTION_FILE  retention policy (JSON)
//	KERN_MEMORY_AUDIT_DIR  override audit persistence directory
//
// It never fails on bad configuration: a malformed policy or retention
// config falls back to the permissive default / no-op retention, and the
// audit trail degrades to in-memory. Memory stays usable even when
// governance config is wrong.
func NewGovernance(root string) *Governance {
	policy, err := PolicyFromEnv()
	if err != nil {
		policy = DefaultPolicy()
	}
	retention, err := RetentionFromEnv()
	if err != nil {
		retention = DefaultRetention()
	}

	trail := NewAuditTrail()
	if dir := os.Getenv("KERN_MEMORY_AUDIT_DIR"); dir != "" {
		trail.WithDir(dir)
	} else if root != "" {
		abs := root
		if a, err := filepath.Abs(root); err == nil {
			abs = a
		}
		// One audit directory per project so trails do not bleed across
		// projects sharing a cache root.
		trail.WithDir(cache.Path("memory-governance", "audit", cache.Hash([]byte(abs))))
	}

	return &Governance{
		Access:    NewAccessControl(policy),
		Audit:     trail,
		Retention: retention,
	}
}

// WithEnvGovernance attaches an environment-configured governance layer to s
// when KERN_MEMORY_GOVERNANCE is truthy ("1", "true", "on", "yes"). The
// policy/retention themselves come from KERN_MEMORY_POLICY[_FILE] and
// KERN_MEMORY_RETENTION[_FILE]; with none set they default to permissive and
// no-op, so enabling governance only starts the audit trail. When the env
// flag is not set, s is returned unchanged (legacy behavior).
func WithEnvGovernance(s *MemoryStore, root string) *MemoryStore {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MEMORY_GOVERNANCE"))) {
	case "1", "true", "on", "yes":
		return s.WithGovernance(NewGovernance(root))
	default:
		return s
	}
}

// WithGovernance attaches a governance layer to the store. Returns s for
// chaining. While attached, every operation is permission-checked and
// audited; when g is nil the store reverts to legacy un-governed behavior.
func (s *MemoryStore) WithGovernance(g *Governance) *MemoryStore {
	s.govMu.Lock()
	s.gov = g
	s.govMu.Unlock()
	return s
}

// Governance returns the attached governance layer, or nil when the store is
// un-governed.
func (s *MemoryStore) Governance() *Governance {
	s.govMu.Lock()
	defer s.govMu.Unlock()
	return s.gov
}

// govSnapshot returns the currently attached governance layer (nil when
// un-governed). Safe to call while holding s.mu: the governance field is
// guarded by its own mutex.
func (s *MemoryStore) govSnapshot() *Governance {
	s.govMu.Lock()
	defer s.govMu.Unlock()
	return s.gov
}

// authorize checks agentID against the attached access control for perm.
// Without governance attached every operation is allowed (legacy). A denial
// is recorded to the audit trail before being returned.
func (s *MemoryStore) authorize(agent string, perm Permission) error {
	gov := s.govSnapshot()
	if gov == nil || gov.Access == nil {
		return nil
	}
	if err := gov.Access.Authorize(agent, perm); err != nil {
		s.recordAudit(AuditEvent{
			AgentID:   agent,
			Operation: opForPermission(perm),
			Allowed:   false,
			Reason:    err.Error(),
		})
		return err
	}
	return nil
}

// recordAudit best-effort writes ev to the attached audit trail. Auditing is
// additive and never fails the operation it describes.
func (s *MemoryStore) recordAudit(ev AuditEvent) {
	gov := s.govSnapshot()
	if gov == nil || gov.Audit == nil {
		return
	}
	if ev.Root == "" {
		ev.Root = s.root
	}
	if err := gov.Audit.Record(ev); err != nil {
		log.Printf("memory: audit record failed: %v", err)
	}
}

// opForPermission maps a permission to the audit operation that represents a
// denial of it.
func opForPermission(p Permission) Operation {
	switch p {
	case PermissionRead:
		return OpRecall
	case PermissionWrite:
		return OpAdd
	case PermissionDelete:
		return OpDelete
	default:
		return OpUpdate
	}
}
