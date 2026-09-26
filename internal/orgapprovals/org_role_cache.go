package orgapprovals

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// The org-role cache lives WITH the store it caches (R3: cache-with-store):
// CachedOrgRolesFor is the mtime+size-validated read seam the rbac package
// uses for org-wins resolution, and AssignOrgRole invalidates the cache
// directly after every successful save — so every same-process write path
// (MCP org-scope assign, enterprise-REST RegisterAgent/role bind, future
// callers) is visible to the very next CheckAgentTool with zero staleness.
//
// Cross-process writes and operator hand-edits cannot invalidate this
// process's cache, so the mtime+size check covers them: the atomic
// temp+rename save gives a fresh mtime, and any role-name length change
// also changes the file size. The residual blind spot — an equal-length
// role-name swap with a mtime frozen by another process — is theoretical
// and is closed for every same-process write path by the direct
// invalidation above.
var (
	orgRolesMu          sync.Mutex
	orgRolesRoot        string
	orgRolesAt          time.Time
	orgRolesSize        int64
	orgRolesMap         map[string]string
	orgRBACCacheCorrupt sync.Once
)

// CachedOrgRolesFor returns the org agent->role map, reloading the store
// only on mtime/size change (missing → empty, corrupt → error). A corrupt
// store is warned once, then fails closed to the caller (which falls back
// to the project role / unassigned posture) — a corrupt store must never
// grant an org role it cannot prove.
func CachedOrgRolesFor(orgRoot string) (map[string]string, error) {
	if orgRoot == "" {
		return map[string]string{}, nil
	}
	orgRolesMu.Lock()
	defer orgRolesMu.Unlock()
	fi, err := os.Stat(OrgRBACPath(orgRoot))
	if err != nil {
		orgRolesRoot, orgRolesAt, orgRolesSize, orgRolesMap = "", time.Time{}, 0, nil
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("orgapprovals: read org rbac store: %w", err)
	}
	if orgRolesRoot == orgRoot && orgRolesAt.Equal(fi.ModTime()) && orgRolesSize == fi.Size() {
		return orgRolesMap, nil
	}
	roles, err := LoadOrgRBACRoles(orgRoot)
	if err != nil {
		orgRBACCacheCorrupt.Do(func() {
			log.Printf("orgapprovals: WARNING: org rbac store %s is corrupt (%v) — org roles are not enforced until it is fixed", OrgRBACPath(orgRoot), err)
		})
		return nil, err
	}
	orgRolesRoot, orgRolesAt, orgRolesSize, orgRolesMap = orgRoot, fi.ModTime(), fi.Size(), roles
	return roles, nil
}

// InvalidateOrgRolesCache forces the next CachedOrgRolesFor to re-read the
// store — the write-side invalidation AssignOrgRole performs after its
// atomic save. Exported so any future org-role write path (enterprise REST,
// CLI, other packages) can invalidate directly instead of waiting for the
// mtime+size check.
func InvalidateOrgRolesCache(orgRoot string) {
	orgRolesMu.Lock()
	defer orgRolesMu.Unlock()
	orgRolesRoot, orgRolesAt, orgRolesSize, orgRolesMap = "", time.Time{}, 0, nil
}
