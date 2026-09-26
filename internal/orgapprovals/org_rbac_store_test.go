package orgapprovals

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestOrgRBACRolesStoreRoundTrip covers the org-scope RBAC store (P13 stage
// 2): a missing store loads as an empty map, a corrupt store FAILS CLOSED
// with an error (never a partial org privilege set), saves are atomic and
// owner-only, nil/empty maps round trip, List agrees with Load, and the
// empty-root (no org configured) semantics keep org scope strictly opt-in.
func TestOrgRBACRolesStoreRoundTrip(t *testing.T) {
	root := t.TempDir()

	// Missing store: empty map, no error.
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		t.Fatalf("missing store must load as empty: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("missing store returned %v, want empty", roles)
	}

	// Corrupt store: fail closed with an error.
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, ".kern", "org-rbac.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrgRBACRoles(root); err == nil {
		t.Fatal("corrupt store must fail closed with an error")
	}

	// Round trip after removing the corrupt file.
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := SaveOrgRBACRoles(root, map[string]string{"agent-a": "admin"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	roles, err = LoadOrgRBACRoles(root)
	if err != nil {
		t.Fatalf("load after save: %v", err)
	}
	if roles["agent-a"] != "admin" {
		t.Fatalf("round trip = %v, want agent-a=admin", roles)
	}
	// List agrees with Load (the display read).
	if listed := ListOrgRBACRoles(root); listed["agent-a"] != "admin" {
		t.Fatalf("ListOrgRBACRoles = %v, want agent-a=admin", listed)
	}
	// OrgRole resolves the single-agent view.
	if role, ok := OrgRole(root, "agent-a"); !ok || role != "admin" {
		t.Fatalf("OrgRole = %q,%v, want admin,true", role, ok)
	}
	if _, ok := OrgRole(root, "nobody"); ok {
		t.Fatal("OrgRole must be unassigned for an unknown agent")
	}

	// Owner-only (0o600): org role assignments reveal who holds org-wide
	// elevated privileges; other local users must not read them.
	info, err := os.Stat(bad)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("org-rbac.json perms = %v, want 0600", info.Mode().Perm())
	}

	// Nil/empty maps round trip as an empty map.
	if err := SaveOrgRBACRoles(root, nil); err != nil {
		t.Fatalf("save nil: %v", err)
	}
	roles, err = LoadOrgRBACRoles(root)
	if err != nil {
		t.Fatalf("load after nil save: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("nil save loaded %v, want empty", roles)
	}

	// Empty root (no org configured): org scope is inactive — reads are
	// empty, writes are refused (strictly opt-in).
	if roles, err := LoadOrgRBACRoles(""); err != nil || len(roles) != 0 {
		t.Fatalf("LoadOrgRBACRoles(\"\") = %v, %v; want empty, nil", roles, err)
	}
	if err := SaveOrgRBACRoles("", map[string]string{"a": "b"}); err == nil {
		t.Fatal("SaveOrgRBACRoles(\"\") must refuse: org scope is opt-in")
	}
	if _, ok := OrgRole("", "agent-a"); ok {
		t.Fatal("OrgRole must be unassigned with no org root")
	}
}

// TestAssignOrgRoleHelper covers the load-current → set → save helper behind
// org-scope assign and the enterprise registry binding: it merges with
// existing assignments (never clobbers) and overwrites on re-assignment.
func TestAssignOrgRoleHelper(t *testing.T) {
	root := t.TempDir()
	if err := AssignOrgRole(root, "agent-1", "admin"); err != nil {
		t.Fatalf("AssignOrgRole: %v", err)
	}
	if err := AssignOrgRole(root, "agent-2", "reviewer"); err != nil {
		t.Fatalf("AssignOrgRole 2: %v", err)
	}
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if roles["agent-1"] != "admin" || roles["agent-2"] != "reviewer" {
		t.Fatalf("merged store = %v, want agent-1=admin, agent-2=reviewer", roles)
	}
	if err := AssignOrgRole(root, "agent-1", "auditor"); err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	if role, _ := OrgRole(root, "agent-1"); role != "auditor" {
		t.Fatalf("re-assigned role = %q, want auditor", role)
	}
	// An empty role clears the binding.
	if err := AssignOrgRole(root, "agent-1", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := OrgRole(root, "agent-1"); ok {
		t.Fatal("empty role must clear the org binding")
	}
}

// TestResolveRoleOrgWins covers the Stage-2 resolution order: an org role
// (org root configured AND the agent has one) beats the per-project role;
// otherwise the project role stands; otherwise unassigned. With no org root
// the result is byte-for-byte the pre-Stage-2 project-only resolution, and a
// corrupt org store fails closed to the project role.
func TestResolveRoleOrgWins(t *testing.T) {
	orgRoot := t.TempDir()
	projectRole, projectAssigned := "junior_dev", true

	// Org root configured with a role for the agent: org wins.
	if err := AssignOrgRole(orgRoot, "agent-1", "admin"); err != nil {
		t.Fatal(err)
	}
	role, source, assigned := ResolveRole(orgRoot, projectRole, projectAssigned, "agent-1")
	if !assigned || role != "admin" || source != "org" {
		t.Fatalf("org-wins: got (%q,%q,%v), want (admin,org,true)", role, source, assigned)
	}

	// Org root configured but NO org role for the agent: project role stands.
	role, source, assigned = ResolveRole(orgRoot, projectRole, projectAssigned, "agent-2")
	if !assigned || role != "junior_dev" || source != "project" {
		t.Fatalf("project fallback: got (%q,%q,%v), want (junior_dev,project,true)", role, source, assigned)
	}

	// Neither org nor project: unassigned.
	role, source, assigned = ResolveRole(orgRoot, "", false, "agent-2")
	if assigned || role != "" || source != "" {
		t.Fatalf("unassigned: got (%q,%q,%v), want unassigned", role, source, assigned)
	}

	// No org root: project resolution unchanged (byte-for-byte compat).
	role, source, assigned = ResolveRole("", projectRole, projectAssigned, "agent-1")
	if !assigned || role != "junior_dev" || source != "project" {
		t.Fatalf("no-org-root: got (%q,%q,%v), want (junior_dev,project,true)", role, source, assigned)
	}
	role, _, assigned = ResolveRole("", "", false, "agent-1")
	if assigned {
		t.Fatalf("no-org-root unassigned must stay unassigned, got %q", role)
	}

	// Corrupt org store fails closed: no org role granted, project stands
	// (a corrupt store must never grant an org role it cannot prove).
	if err := os.WriteFile(filepath.Join(orgRoot, ".kern", "org-rbac.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	role, source, assigned = ResolveRole(orgRoot, projectRole, projectAssigned, "agent-1")
	if !assigned || role != "junior_dev" || source != "project" {
		t.Fatalf("corrupt store must fail closed to project role, got (%q,%q,%v)", role, source, assigned)
	}
}

// TestAssignOrgRoleConcurrentNoLostUpdate covers the org-assign
// serialization (orgAssignMu, finding 4): N goroutines assigning N distinct
// agents must all survive on disk — none lost to a racing rename — exactly
// like the project-path TestAssignConcurrentNoLostUpdate in the rbac
// package.
func TestAssignOrgRoleConcurrentNoLostUpdate(t *testing.T) {
	root := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- AssignOrgRole(root, fmt.Sprintf("org-agent-%d", i), "developer")
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent org assign: %v", err)
		}
	}
	roles, err := LoadOrgRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadOrgRBACRoles: %v", err)
	}
	if len(roles) != n {
		t.Fatalf("disk has %d org assignments, want %d (lost update): %v", len(roles), n, roles)
	}
	for i := 0; i < n; i++ {
		if roles[fmt.Sprintf("org-agent-%d", i)] != "developer" {
			t.Fatalf("org-agent-%d missing/wrong on disk: %v", i, roles)
		}
	}
}

// TestCachedOrgRolesReloadsOnChange covers the finding-5 mtime cache (now
// living with the store it caches, R3): a missing store reads as empty and a
// store created afterwards is picked up; an out-of-band change (mtime/size
// change) reloads; an unchanged store serves the cached map; no org root →
// empty map with zero disk access.
func TestCachedOrgRolesReloadsOnChange(t *testing.T) {
	orgRoot := t.TempDir()
	InvalidateOrgRolesCache(orgRoot) // pristine cache
	// No org root → empty map, no disk access.
	if roles, err := CachedOrgRolesFor(""); err != nil || len(roles) != 0 {
		t.Fatalf("CachedOrgRolesFor(\"\") = %v, %v; want empty", roles, err)
	}
	// Missing store → empty map (and the cache resets so a later-created
	// store is picked up).
	if roles, err := CachedOrgRolesFor(orgRoot); err != nil || len(roles) != 0 {
		t.Fatalf("CachedOrgRolesFor(missing) = %v, %v; want empty", roles, err)
	}
	// Seed the store; the next read must reload it (created after the miss).
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"a1": "admin"}); err != nil {
		t.Fatal(err)
	}
	roles, err := CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "admin" {
		t.Fatalf("CachedOrgRolesFor after seed = %v, %v; want a1=admin", roles, err)
	}
	// An out-of-band change (different content ⇒ different size) must be
	// picked up: org-role changes take effect within one mtime granularity.
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"a1": "reviewer"}); err != nil {
		t.Fatal(err)
	}
	roles, err = CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "reviewer" {
		t.Fatalf("CachedOrgRolesFor must reload on change, got %v, %v; want a1=reviewer", roles, err)
	}
	// Unchanged store → the cached map is served again, still fresh.
	roles, err = CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "reviewer" {
		t.Fatalf("cached read = %v, %v; want a1=reviewer", roles, err)
	}
}

// TestAssignOrgRoleInvalidatesCache covers the R3 write-side invalidation at
// the store level: AssignOrgRole (the write path the MCP assign AND the
// enterprise-REST RegisterAgent/role bind share) invalidates the cached
// org-role map after its atomic save, so the next CachedOrgRolesFor re-reads
// the store even when mtime AND size are frozen to the cached values (an
// mtime+size-only cache would serve the stale role).
func TestAssignOrgRoleInvalidatesCache(t *testing.T) {
	orgRoot := t.TempDir()
	if err := AssignOrgRole(orgRoot, "swap-a", "architect"); err != nil {
		t.Fatalf("AssignOrgRole architect: %v", err)
	}
	if roles, err := CachedOrgRolesFor(orgRoot); err != nil || roles["swap-a"] != "architect" {
		t.Fatalf("warm read = %v, %v; want swap-a=architect", roles, err)
	}
	orgRolesMu.Lock()
	warm := orgRolesRoot == orgRoot && orgRolesMap["swap-a"] == "architect"
	orgRolesMu.Unlock()
	if !warm {
		t.Fatal("cache must hold architect after the warm read")
	}
	// Capture the WARM stat (the values the cache holds) so the file can be
	// rewound to them after the write: an mtime+size-only cache would then
	// see no change and serve the stale role.
	path := OrgRBACPath(orgRoot)
	warmFi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat org rbac store: %v", err)
	}
	// Equal-length role name => same file size; mtime is rewound below.
	if err := AssignOrgRole(orgRoot, "swap-a", "developer"); err != nil {
		t.Fatalf("AssignOrgRole developer: %v", err)
	}
	orgRolesMu.Lock()
	invalidated := orgRolesRoot == ""
	orgRolesMu.Unlock()
	if !invalidated {
		t.Fatal("AssignOrgRole must invalidate the cached org-role map after a successful save")
	}
	// Rewind mtime to the WARM value: mtime and size are now both identical
	// to what the cache holds, so only the write-side invalidation can make
	// the next read see developer.
	if err := os.Chtimes(path, warmFi.ModTime(), warmFi.ModTime()); err != nil {
		t.Fatalf("chtimes rewind: %v", err)
	}
	roles, err := CachedOrgRolesFor(orgRoot)
	if err != nil || roles["swap-a"] != "developer" {
		t.Fatalf("next read must see the swapped role via invalidation, got %v, %v; want swap-a=developer", roles, err)
	}
}

// TestCachedOrgRolesForEmptyRootNoDiskAccess guards the no-org-root fast
// path: with an empty root the cache returns empty without touching disk.
func TestCachedOrgRolesForEmptyRootNoDiskAccess(t *testing.T) {
	if roles, err := CachedOrgRolesFor(""); err != nil || len(roles) != 0 {
		t.Fatalf("CachedOrgRolesFor(\"\") = %v, %v; want empty, nil", roles, err)
	}
}

// TestCacheMtimeGranularityDetectsEqualLengthSwap is the residual
// cross-process blind-spot guard: an equal-length role swap with a FROZEN
// mtime and no write-side invalidation is NOT detected by the mtime+size
// check alone (the documented theoretical limit — every same-process write
// path closes it via AssignOrgRole's direct invalidation). It pins the
// cache's exact semantics so a future change to mtime+size hashing is a
// deliberate decision, not an accident.
func TestCacheMtimeGranularityDetectsEqualLengthSwap(t *testing.T) {
	orgRoot := t.TempDir()
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"x": "architect"}); err != nil {
		t.Fatal(err)
	}
	roles, err := CachedOrgRolesFor(orgRoot)
	if err != nil || roles["x"] != "architect" {
		t.Fatalf("warm read = %v, %v; want x=architect", roles, err)
	}
	fi, err := os.Stat(OrgRBACPath(orgRoot))
	if err != nil {
		t.Fatal(err)
	}
	// Out-of-band equal-length write (simulating another process) with the
	// mtime frozen to the cached value: the mtime+size check alone serves the
	// stale role — the documented residual that the same-process direct
	// invalidation eliminates.
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"x": "developer"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(OrgRBACPath(orgRoot), fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	roles, err = CachedOrgRolesFor(orgRoot)
	if err != nil || roles["x"] != "architect" {
		t.Fatalf("mtime+size-only check must serve the cached role (documented residual), got %v, %v", roles, err)
	}
}

// TestCacheReloadsWithinMtimeGranularity pins that a cross-process write
// with a FRESH mtime (the normal atomic rename case) IS picked up — the
// cross-process path stays correct for the common case.
func TestCacheReloadsWithinMtimeGranularity(t *testing.T) {
	orgRoot := t.TempDir()
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"x": "architect"}); err != nil {
		t.Fatal(err)
	}
	if roles, err := CachedOrgRolesFor(orgRoot); err != nil || roles["x"] != "architect" {
		t.Fatalf("warm read = %v, %v", roles, err)
	}
	// Give the mtime a chance to advance past the cached value.
	time.Sleep(10 * time.Millisecond)
	if err := SaveOrgRBACRoles(orgRoot, map[string]string{"x": "developer"}); err != nil {
		t.Fatal(err)
	}
	if roles, err := CachedOrgRolesFor(orgRoot); err != nil || roles["x"] != "developer" {
		t.Fatalf("cross-process write with fresh mtime must reload, got %v, %v; want x=developer", roles, err)
	}
}
