package orgapprovals

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// OrgRBACPath returns the org-scope RBAC role-assignment store at
// <org-root>/.kern/org-rbac.json — next to the org policy document under the
// standard "kern generated" gitignore section. It holds org-wide agent→role
// assignments that hold across every project rooted under the org (the org
// admin's assignments, resolved with org-wins precedence over per-project
// roles).
func OrgRBACPath(root string) string {
	return filepath.Join(root, ".kern", "org-rbac.json")
}

// SaveOrgRBACRoles atomically persists the org agent->role assignment map to
// <org-root>/.kern/org-rbac.json, using the same write discipline as the
// project RBAC store: process-wide per-path lock + cross-process flock, then
// a unique temp-file + atomic rename, owner-only (0o600). A failed write is
// surfaced (fail loud), never silently swallowed: an org assignment that
// exists only in memory is exactly the "projects silently enforce per-project
// roles instead of the org role" class of regression this store prevents. A
// nil map is persisted as an empty object. An empty root (no org configured)
// refuses the write — org scope is strictly opt-in.
func SaveOrgRBACRoles(root string, roles map[string]string) error {
	if root == "" {
		return fmt.Errorf("orgapprovals: no org root configured (set %s)", governance.OrgRootEnv)
	}
	path := OrgRBACPath(root)
	fl, err := cache.LockFile(path)
	if err != nil {
		return fmt.Errorf("orgapprovals: lock org rbac store: %w", err)
	}
	defer fl.Unlock()
	pl := cache.PathLock(path)
	pl.Lock()
	defer pl.Unlock()

	if roles == nil {
		roles = map[string]string{}
	}
	data, err := json.MarshalIndent(roles, "", "  ")
	if err != nil {
		return fmt.Errorf("orgapprovals: encode org rbac roles: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("orgapprovals: create org rbac store dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".org-rbac-tmp-*")
	if err != nil {
		return fmt.Errorf("orgapprovals: create org rbac store temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: write org rbac store temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: close org rbac store temp: %w", err)
	}
	// Owner-only (0o600): org role assignments reveal which agents hold
	// elevated privileges org-wide; other local users must not read them
	// (mirrors the rbac store's chmod).
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: chmod org rbac store: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: rename org rbac store: %w", err)
	}
	return nil
}

// LoadOrgRBACRoles reads the org agent->role assignment map from
// <org-root>/.kern/org-rbac.json. A missing store or empty file is not an
// error and returns an empty map (no org roles yet). A corrupt store FAILS
// CLOSED with an error instead of returning a partial map: the caller falls
// back to the pre-org posture (per-project roles / unassigned), never a
// half-restored org privilege set. An empty root (no org configured) returns
// an empty map — org scope is strictly opt-in.
func LoadOrgRBACRoles(root string) (map[string]string, error) {
	if root == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(OrgRBACPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("orgapprovals: read org rbac store: %w", err)
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	var roles map[string]string
	if err := json.Unmarshal(data, &roles); err != nil {
		return nil, fmt.Errorf("orgapprovals: decode org rbac store: %w", err)
	}
	return roles, nil
}

// ListOrgRBACRoles is the display-oriented read of the org role store: it
// returns the same agent->role map as LoadOrgRBACRoles for the listing
// surfaces (org agents page/REST). A corrupt store fails closed for display
// too — no org roles are shown — and an empty root yields an empty map.
func ListOrgRBACRoles(root string) map[string]string {
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		return map[string]string{}
	}
	return roles
}

// AssignOrgRole persists a single agent->role assignment to the org role
// store, merging with existing assignments (load-current → set → save, the
// same snapshot semantics as the project assign path). It is the write path
// behind org-scope assign and the enterprise registry binding. An empty role
// clears the agent's org role. Runs under orgAssignMu so concurrent assigns
// cannot lose one another (the flock alone covers only the save).
//
// After the atomic save the org-role cache (CachedOrgRolesFor) is invalidated
// directly (R3), so EVERY same-process write path — the MCP org-scope assign,
// the enterprise-REST RegisterAgent/role bind, future callers — is visible to
// the very next rbac.CheckAgentTool with zero staleness, even when the
// mtime+size check cannot detect the change (equal-length role names with a
// frozen mtime). Cross-process writes and operator hand-edits keep the
// mtime+size check (documented in org_role_cache.go).
func AssignOrgRole(root, agentID, role string) error {
	orgAssignMu.Lock()
	defer orgAssignMu.Unlock()
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		return err
	}
	roles[agentID] = role
	if err := SaveOrgRBACRoles(root, roles); err != nil {
		return err
	}
	InvalidateOrgRolesCache(root)
	return nil
}

// OrgRole returns the org role assigned to an agent in the org role store,
// and whether one is assigned. When no org root is configured the org scope
// is inactive and nothing is assigned; a corrupt store fails closed to
// unassigned.
func OrgRole(root, agentID string) (string, bool) {
	if root == "" {
		return "", false
	}
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		return "", false
	}
	role, ok := roles[agentID]
	if !ok || role == "" {
		return "", false
	}
	return role, true
}

// orgAssignMu serializes the org assign load→mutate→save sequence (finding
// 4); the store flock alone covers only the save.
var orgAssignMu sync.Mutex

// ResolveRole implements the org-wins RBAC resolution order used by every
// enforcement point:
//
//  1. an org role — when an org root is configured AND the agent has one —
//     wins over any per-project assignment (central governance: the org
//     admin's assignment takes precedence);
//  2. else the caller-supplied per-project role stands (projectRole and
//     projectAssigned come from the caller's authoritative in-memory map, so
//     org mode cannot change how project roles resolve — with no org root
//     this is byte-for-byte the pre-Stage-2 result);
//  3. else unassigned (the legacy permit-all / default-deny posture).
//
// A corrupt org role store fails closed: the agent falls back to its project
// role (or unassigned) and the corruption is warned once (shared with the
// CachedOrgRolesFor path) — a corrupt store must never grant an org role it
// cannot prove.
func ResolveRole(orgRoot string, projectRole string, projectAssigned bool, agentID string) (role, source string, assigned bool) {
	if orgRoot != "" {
		roles, err := LoadOrgRBACRoles(orgRoot)
		if err != nil {
			orgRBACCacheCorrupt.Do(func() {
				log.Printf("orgapprovals: WARNING: org rbac store %s is corrupt (%v) — org roles are not enforced until it is fixed", OrgRBACPath(orgRoot), err)
			})
		} else if role, ok := roles[agentID]; ok && role != "" {
			return role, "org", true
		}
	}
	if projectAssigned {
		return projectRole, "project", true
	}
	return "", "", false
}
