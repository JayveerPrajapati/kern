// Org-level user registry + RBAC backing store (Feature Batch G). Users are
// org-wide identities with an RBAC role and an enabled flag; every
// user-management mutation records an append-only per-user audit entry
// ({Action, By, At, Detail}). The registry is the role store — permission
// evaluation lives in governance.RoleAllowsOrgAction / RequireOrgRole — and
// is optional/absent-safe: when the org-level profile disables UserRegistry
// (e.g. ProfileBasic) or the server runs without a storage backend, the API
// degrades to errors/nil instead of panicking.

package enterprise

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// User is an org-wide user identity: an ID, an RBAC role, an enabled flag,
// and the timestamps of creation and last update.
type User struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserAuditEntry is one entry in a user's append-only audit trail.
type UserAuditEntry struct {
	Action string    `json:"action"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail"`
}

// userStoreKey and userAuditStoreKey namespace the registry's persisted
// records in the shared org store (storage.Store): users live under
// "user-<id>" and their audit trails under "user-audit-<id>". The storage
// layer rejects path separators in keys, so the flat prefixed scheme keeps
// every record a single safe filename (e.g. user-alice.json) while staying
// unambiguous. Each file is written atomically (temp-file rename).
func userStoreKey(id string) string      { return "user-" + id }
func userAuditStoreKey(id string) string { return "user-audit-" + id }

// validUserID mirrors the storage layer's key-safety rule so a persisted
// user record can never escape the store directory or collide across path
// boundaries.
func validUserID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

// ensureUsersLoadedLocked replays the persisted registry from the shared org
// store into memory on first access, so a server restarted with the same
// store recovers its users and their audit trails. Safe to call repeatedly;
// the caller must hold s.mu.
func (s *Server) ensureUsersLoadedLocked() {
	if s.usersLoaded {
		return
	}
	s.usersLoaded = true
	if s.store == nil {
		return
	}
	entries, err := s.store.List(context.Background())
	if err != nil {
		return // no store state yet; in-memory registry stays authoritative
	}
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Key, "user-audit-"):
			var trail []UserAuditEntry
			if err := storage.UnmarshalValue(e.Value, &trail); err == nil {
				s.userAudits[strings.TrimPrefix(e.Key, "user-audit-")] = trail
			}
		case strings.HasPrefix(e.Key, "user-"):
			var u User
			if err := storage.UnmarshalValue(e.Value, &u); err == nil && u.ID != "" {
				cp := u
				s.users[u.ID] = &cp
			}
		}
	}
}

// putStoreValue marshals v and stores it atomically in the org store.
func putStoreValue(ctx context.Context, st storage.Store, key string, v any) error {
	raw, err := storage.MarshalValue(v)
	if err != nil {
		return err
	}
	return st.Put(ctx, key, raw)
}

// persistUserLocked writes the user record and its audit trail to the shared
// org store (when one is attached). Both writes are atomic at the store
// layer; a persistence failure surfaces as an error but the in-memory state
// is already updated (the caller decides whether to roll back).
func (s *Server) persistUserLocked(id string) error {
	if s.store == nil {
		return nil
	}
	ctx := context.Background()
	u, ok := s.users[id]
	if !ok {
		return nil
	}
	if err := putStoreValue(ctx, s.store, userStoreKey(id), *u); err != nil {
		return fmt.Errorf("enterprise: persist user %q: %w", id, err)
	}
	if trail, exists := s.userAudits[id]; exists {
		if err := putStoreValue(ctx, s.store, userAuditStoreKey(id), trail); err != nil {
			return fmt.Errorf("enterprise: persist audit trail for user %q: %w", id, err)
		}
	}
	return nil
}

// recordUserAuditLocked appends an entry to the user's audit trail and
// persists it. The caller must hold s.mu.
func (s *Server) recordUserAuditLocked(id, action, by, detail string) {
	entry := UserAuditEntry{Action: action, By: by, At: time.Now().UTC(), Detail: detail}
	s.userAudits[id] = append(s.userAudits[id], entry)
	if s.store != nil {
		if err := putStoreValue(context.Background(), s.store, userAuditStoreKey(id), s.userAudits[id]); err != nil {
			// Audit recording is best-effort on persistence: the in-memory
			// trail is authoritative for the running process.
			_ = err
		}
	}
}

// AddUser registers a new org user with the given role. A duplicate ID is an
// error; the per-user audit entry is recorded with By="system".
func (s *Server) AddUser(id, role string) error {
	return s.AddUserBy(id, role, "system")
}

// AddUserBy registers a new org user, recording the acting identity in the
// user's audit trail. An empty ID or role is rejected, as is a duplicate ID.
func (s *Server) AddUserBy(id, role, by string) error {
	if !s.orgFeatures().UserRegistry {
		return fmt.Errorf("enterprise: user registry disabled by profile %q", s.profile)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("enterprise: user ID must not be empty")
	}
	if !validUserID(id) {
		return fmt.Errorf("enterprise: user ID %q is not a safe registry key", id)
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("enterprise: user %q role must not be empty", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureUsersLoadedLocked()
	if _, exists := s.users[id]; exists {
		return fmt.Errorf("enterprise: user %q already exists", id)
	}
	now := time.Now().UTC()
	s.users[id] = &User{ID: id, Role: role, Enabled: true, CreatedAt: now, UpdatedAt: now}
	s.recordUserAuditLocked(id, "user-add", by, "role "+role)
	if err := s.persistUserLocked(id); err != nil {
		return err
	}
	return nil
}

// ListUsers returns all org users sorted by ID, or nil when the org-level
// profile disables UserRegistry.
func (s *Server) ListUsers() []User {
	if !s.orgFeatures().UserRegistry {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.ensureUsersLoadedLocked()
	ids := slices.Sorted(maps.Keys(s.users))
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.users[id])
	}
	return out
}

// SetRole updates a user's role, recording an audit entry with By="system".
func (s *Server) SetRole(id, role string) error {
	return s.SetRoleBy(id, role, "system")
}

// SetRoleBy updates a user's role, recording the acting identity. An unknown
// user or an empty role is an error.
func (s *Server) SetRoleBy(id, role, by string) error {
	if !s.orgFeatures().UserRegistry {
		return fmt.Errorf("enterprise: user registry disabled by profile %q", s.profile)
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("enterprise: role must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureUsersLoadedLocked()
	u, exists := s.users[id]
	if !exists {
		return fmt.Errorf("enterprise: user %q not found", id)
	}
	old := u.Role
	u.Role = role
	u.UpdatedAt = time.Now().UTC()
	s.recordUserAuditLocked(id, "user-role", by, old+" -> "+role)
	if err := s.persistUserLocked(id); err != nil {
		return err
	}
	return nil
}

// DisableUser disables a user (Enabled=false), recording an audit entry with
// By="system".
func (s *Server) DisableUser(id string) error {
	return s.DisableUserBy(id, "system")
}

// DisableUserBy disables a user, recording the acting identity. An unknown
// user is an error; disabling an already-disabled user is idempotent (the
// audit entry is still recorded).
func (s *Server) DisableUserBy(id, by string) error {
	if !s.orgFeatures().UserRegistry {
		return fmt.Errorf("enterprise: user registry disabled by profile %q", s.profile)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureUsersLoadedLocked()
	u, exists := s.users[id]
	if !exists {
		return fmt.Errorf("enterprise: user %q not found", id)
	}
	u.Enabled = false
	u.UpdatedAt = time.Now().UTC()
	s.recordUserAuditLocked(id, "user-disable", by, "disabled")
	if err := s.persistUserLocked(id); err != nil {
		return err
	}
	return nil
}

// UserAudit returns the user's append-only audit trail, oldest first. An
// unknown user yields an empty (non-nil) slice. Returns nil when the
// org-level profile disables UserRegistry.
func (s *Server) UserAudit(id string) []UserAuditEntry {
	if !s.orgFeatures().UserRegistry {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.ensureUsersLoadedLocked()
	trail, ok := s.userAudits[id]
	if !ok {
		return []UserAuditEntry{}
	}
	return append([]UserAuditEntry(nil), trail...)
}

// UserRole returns the user's role and whether the user exists. The RBAC
// lookups (kern_org_user handlers, web approvals) use this; an unknown user
// yields ("", false).
func (s *Server) UserRole(id string) (string, bool) {
	if !s.orgFeatures().UserRegistry {
		return "", false
	}
	// P13 stage 2: an org role (org-rbac.json) wins over the user registry.
	if s.orgRoot != "" {
		if role, ok := orgapprovals.OrgRole(s.orgRoot, id); ok {
			return role, true
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.ensureUsersLoadedLocked()
	u, ok := s.users[id]
	if !ok {
		return "", false
	}
	return u.Role, true
}
