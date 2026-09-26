package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

func TestHandleRBAC(t *testing.T) {
	ctx := context.Background()

	// 1. Evaluate allowed tool for developer
	res, err := Handle(ctx, map[string]any{
		"action": "evaluate",
		"role":   "developer",
		"tool":   "kern_exec",
	})
	if err != nil {
		t.Fatalf("Handle evaluate failed: %v", err)
	}
	if !strings.Contains(res, "ALLOWED") {
		t.Errorf("expected ALLOWED for developer on kern_exec, got: %s", res)
	}

	// 2. Evaluate denied tool for junior_dev
	resDenied, err := Handle(ctx, map[string]any{
		"action": "evaluate",
		"role":   "junior_dev",
		"tool":   "kern_exec",
	})
	if err != nil {
		t.Fatalf("Handle evaluate junior_dev failed: %v", err)
	}
	if !strings.Contains(resDenied, "DENIED") {
		t.Errorf("expected DENIED for junior_dev on kern_exec, got: %s", resDenied)
	}

	// 3. Assign role fails closed without the operator opt-in env var.
	_, err = Handle(ctx, map[string]any{
		"action":   "assign",
		"agent_id": "bob",
		"role":     "admin",
	})
	if err == nil {
		t.Fatalf("expected assign to fail closed without KERN_ALLOW_RBAC_ASSIGN, got nil error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected fail-closed message mentioning disabled, got: %v", err)
	}
}

func TestHandleRBACAssignEnabled(t *testing.T) {
	ctx := context.Background()
	if err := os.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	defer os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")
	defer ResetMemory()
	root := t.TempDir() // persist into a temp root, never the package dir
	primaryRoot = root  // in-package hook: this process is rooted here
	resAssign, err := Handle(ctx, map[string]any{
		"action":   "assign",
		"agent_id": "bob",
		"role":     "admin",
		"root":     root,
	})
	if err != nil {
		t.Fatalf("Handle assign failed: %v", err)
	}
	if !strings.Contains(resAssign, "Assigned role \"admin\"") {
		t.Errorf("expected role assigned, got: %s", resAssign)
	}
}

func TestCheckAgentTool(t *testing.T) {
	ResetMemory()
	defer ResetMemory()

	// An agent with no assigned role is always permitted (legacy trust model).
	if allowed, reason := CheckAgentTool("unassigned-1", "kern_exec"); !allowed {
		t.Errorf("expected unassigned agent to be allowed, got reason %q", reason)
	}

	// Assign junior_dev to an agent (operator opt-in path).
	if err := os.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	defer os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")
	root := t.TempDir() // persist into a temp root, never the package dir
	primaryRoot = root  // in-package hook: assigns here activate in memory
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "junior-1",
		"role":     "junior_dev",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	// Assigned role restricts: junior_dev denies kern_exec.
	if allowed, _ := CheckAgentTool("junior-1", "kern_exec"); allowed {
		t.Errorf("expected junior_dev to be denied kern_exec")
	}
	// ...and allows a tool on its AllowedTools list.
	if allowed, reason := CheckAgentTool("junior-1", "kern_compact_file"); !allowed {
		t.Errorf("expected junior_dev to be allowed kern_compact_file, got reason %q", reason)
	}
	// DeniedTools wins even when the role otherwise allows broad access.
	if allowed, _ := CheckAgentTool("junior-1", "kern_lock"); allowed {
		t.Errorf("expected junior_dev to be denied kern_lock (DeniedTools)")
	}

	// admin's "*" AllowedTools grants everything.
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "boss",
		"role":     "admin",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign admin: %v", err)
	}
	if allowed, reason := CheckAgentTool("boss", "kern_exec"); !allowed {
		t.Errorf("expected admin to be allowed kern_exec, got reason %q", reason)
	}

	// Glob-suffix matching (architect allows kern_memory*).
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "arch-1",
		"role":     "architect",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign architect: %v", err)
	}
	if allowed, reason := CheckAgentTool("arch-1", "kern_memory_recall"); !allowed {
		t.Errorf("expected architect to be allowed kern_memory_recall (glob), got reason %q", reason)
	}
	if allowed, _ := CheckAgentTool("arch-1", "kern_exec"); allowed {
		t.Errorf("expected architect to be denied kern_exec")
	}
}

// TestCheckAgentToolDefaultDenyOptIn covers the KERN_RBAC_DEFAULT_DENY=1
// opt-in: unset (default) keeps the legacy permit-all loopback trust model;
// set, unassigned agents map to the built-in read-only "reviewer" role.
// Explicitly assigned roles are unaffected either way. Uses os.Setenv like
// the other env-gated tests here, so it must not run in parallel.
func TestCheckAgentToolDefaultDenyOptIn(t *testing.T) {
	ResetMemory()
	defer ResetMemory()

	// Opt-in off (default): unassigned agents are always permitted.
	os.Unsetenv("KERN_RBAC_DEFAULT_DENY")
	if allowed, _ := CheckAgentTool("unassigned-1", "kern_exec"); !allowed {
		t.Error("KERN_RBAC_DEFAULT_DENY unset: unassigned agent must be permitted (legacy trust model)")
	}

	// Opt-in on: unassigned agents get the read-only reviewer role — read
	// tools pass, write/exec tools are denied.
	if err := os.Setenv("KERN_RBAC_DEFAULT_DENY", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	defer os.Unsetenv("KERN_RBAC_DEFAULT_DENY")

	if allowed, reason := CheckAgentTool("unassigned-1", "kern_compact_file"); !allowed {
		t.Errorf("default-deny: reviewer role must allow kern_compact_file, got reason %q", reason)
	}
	if allowed, reason := CheckAgentTool("unassigned-1", "kern_exec"); allowed {
		t.Errorf("default-deny: unassigned agent must be denied kern_exec, got allowed (reason %q)", reason)
	}
	if allowed, _ := CheckAgentTool("unassigned-1", "kern_fix"); allowed {
		t.Error("default-deny: reviewer role must deny kern_fix")
	}
	if allowed, _ := CheckAgentTool("unassigned-1", "kern_memory_add"); allowed {
		t.Error("default-deny: reviewer role must deny kern_memory_add (not in its AllowedTools)")
	}

	// Explicitly assigned roles are unaffected by the opt-in.
	if err := os.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	defer os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")
	root := t.TempDir() // in-package hook: this process is rooted here
	primaryRoot = root
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "boss-1",
		"role":     "admin",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign admin: %v", err)
	}
	if allowed, reason := CheckAgentTool("boss-1", "kern_exec"); !allowed {
		t.Errorf("assigned admin must be unaffected by default-deny, got denied (reason %q)", reason)
	}
}

// TestCheckAgentToolDefaultDenyCoversDefaultPrincipal is the audit
// iteration-2 finding-1 regression: the dispatch funnel resolves a missing
// agent_id to the built-in "default" principal, which was pre-seeded as
// "developer" (AllowedTools ["*"]) and therefore never hit the deny branch.
// With the pre-seed removed, "default" counts as unassigned — KERN_RBAC_
// DEFAULT_DENY=1 denies it like any other unassigned agent, while legacy mode
// (env unset) keeps permitting it. Serial (t.Setenv, no t.Parallel).
func TestCheckAgentToolDefaultDenyCoversDefaultPrincipal(t *testing.T) {
	ResetMemory()
	defer ResetMemory()

	// Legacy mode (env unset): the "default" principal stays permitted —
	// removing the pre-seed must not change the default trust model.
	os.Unsetenv("KERN_RBAC_DEFAULT_DENY")
	if allowed, reason := CheckAgentTool("default", "kern_exec"); !allowed {
		t.Errorf("default-deny unset: 'default' principal must be permitted (legacy trust), got denied (%q)", reason)
	}

	// Default-deny on: "default" + kern_exec is DENIED (it is not assigned).
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	if allowed, reason := CheckAgentTool("default", "kern_exec"); allowed {
		t.Errorf("default-deny: 'default' principal must be denied kern_exec, got allowed (reason %q)", reason)
	}
	// The read-only reviewer surface is available to it — the default MCP
	// workflow (meta/explore/context/impact/search) must keep working.
	for _, tool := range []string{"kern_meta", "kern_explore", "kern_context", "kern_impact", "kern_search", "kern_plan"} {
		if allowed, reason := CheckAgentTool("default", tool); !allowed {
			t.Errorf("default-deny: 'default' principal must be allowed %s (reviewer surface), got denied (%s)", tool, reason)
		}
	}
	// Write/execute/mutate tools stay denied for it.
	for _, tool := range []string{"kern_execute", "kern_sandbox", "kern_deploy", "kern_rename", "kern_validate"} {
		if allowed, reason := CheckAgentTool("default", tool); allowed {
			t.Errorf("default-deny: 'default' principal must be denied %s, got allowed (reason %q)", tool, reason)
		}
	}

	// An EXPLICITLY-assigned developer keeps full access under default-deny.
	// assign is auto-permitted when default-deny is on (finding 3), so no
	// KERN_ALLOW_RBAC_ASSIGN is needed here.
	root := t.TempDir()
	primaryRoot = root // in-package hook: assigns here activate in memory
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "dev-2",
		"role":     "developer",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign developer under default-deny: %v", err)
	}
	if allowed, reason := CheckAgentTool("dev-2", "kern_exec"); !allowed {
		t.Errorf("default-deny: explicitly-assigned developer must be allowed kern_exec, got denied (%s)", reason)
	}
}

// TestAssignPreservesHandEditedRoles covers audit iteration-4 finding 2:
// assign snapshotted the IN-MEMORY agentRoles, so an operator hand-edit of
// <root>/.kern/rbac.json while a process runs was silently DISCARDED by the
// next assignment. The fix re-loads the persisted store under assignMu as the
// snapshot base, so out-of-band edits (and other processes' assignments)
// survive: assign A → hand-edit adds B → assign C → disk holds A, B, C —
// and (iteration-5 finding 1) the full-map commit ACTIVATES B in memory.
func TestAssignPreservesHandEditedRoles(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	root := t.TempDir()
	primaryRoot = root // in-package hook: same-root assigns activate in memory
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	// 1. Assign agent A via Handle (persists A to the store).
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "agent-a",
		"role":     "developer",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign A: %v", err)
	}

	// 2. Hand-edit the persisted rbac.json out-of-band: add agent B directly,
	// exactly like an operator editing the file while the process runs.
	roles, err := governance.LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadRBACRoles: %v", err)
	}
	roles["agent-b"] = "auditor"
	data, err := json.MarshalIndent(roles, "", "  ")
	if err != nil {
		t.Fatalf("marshal hand-edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "rbac.json"), data, 0o600); err != nil {
		t.Fatalf("hand-edit write: %v", err)
	}

	// 3. Assign agent C via Handle — must NOT clobber the hand-edit.
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "agent-c",
		"role":     "junior_dev",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign C: %v", err)
	}

	// 4. Reload from disk: A, B, and C all survived — the hand-edit was not
	// discarded by the assign that followed it.
	roles, err = governance.LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadRBACRoles final: %v", err)
	}
	if roles["agent-a"] != "developer" || roles["agent-b"] != "auditor" || roles["agent-c"] != "junior_dev" {
		t.Fatalf("hand-edit lost: disk = %v, want agent-a=developer, agent-b=auditor, agent-c=junior_dev", roles)
	}

	// 5. In-memory activation (audit iteration-5 finding 1): the assign
	// commits the full disk map, so the running process enforces the
	// hand-edit without a restart — the in-memory map carries agent B.
	rbacMu.RLock()
	got := agentRoles["agent-b"]
	rbacMu.RUnlock()
	if got != "auditor" {
		t.Fatalf("hand-edit not activated in memory: agentRoles[agent-b] = %q, want auditor", got)
	}
}

// TestAssignConcurrentNoLostUpdate covers audit iteration-3 finding 4: the
// assign snapshot→save→commit sequence is NOT atomic as-is, so two
// concurrent assigns to DIFFERENT agents can interleave and the loser's file
// rename lands last — the restart then loses one assignment. With the
// sequence serialized under assignMu, N goroutines assigning N distinct
// agents must all survive: reload-from-disk (LoadRBACRoles) shows all N.
func TestAssignConcurrentNoLostUpdate(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	root := t.TempDir()
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Handle(context.Background(), map[string]any{
				"action":   "assign",
				"agent_id": fmt.Sprintf("agent-%d", i),
				"role":     "developer",
				"root":     root,
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent assign: %v", err)
		}
	}

	// Reload from disk exactly like a restart would: all N assignments must
	// be present — none lost to a racing rename.
	roles, err := governance.LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadRBACRoles: %v", err)
	}
	if len(roles) != n {
		t.Fatalf("disk has %d assignments, want %d (lost update): %v", len(roles), n, roles)
	}
	for i := 0; i < n; i++ {
		if roles[fmt.Sprintf("agent-%d", i)] != "developer" {
			t.Fatalf("agent-%d missing/wrong on disk: %v", i, roles)
		}
	}
}

// TestAssignPersistsAcrossRestart covers finding 8: role assignments are
// persisted to the atomic <root>/.kern/rbac.json store, so default-deny
// enforcement survives the process that created them. Also proves a second
// assignment merges with (never clobbers) the first.
func TestAssignPersistsAcrossRestart(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	root := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1") // auto-permits assign (finding 3)

	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "survivor-1",
		"role":     "junior_dev",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	roles, err := governance.LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadRBACRoles: %v", err)
	}
	if roles["survivor-1"] != "junior_dev" {
		t.Fatalf("persisted roles = %v, want survivor-1=junior_dev", roles)
	}

	// A second assignment must merge into the same store, not overwrite it.
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "survivor-2",
		"role":     "auditor",
		"root":     root,
	}); err != nil {
		t.Fatalf("assign 2: %v", err)
	}
	roles, err = governance.LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("LoadRBACRoles 2: %v", err)
	}
	if roles["survivor-1"] != "junior_dev" || roles["survivor-2"] != "auditor" {
		t.Fatalf("merge failed: %v", roles)
	}
	// The store is owner-only: assignments reveal who holds elevated roles.
	info, err := os.Stat(filepath.Join(root, ".kern", "rbac.json"))
	if err != nil {
		t.Fatalf("stat rbac.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rbac.json perms = %v, want 0600", info.Mode().Perm())
	}
}

// TestAssignCrossRootPersistsWithoutMemorySwap covers the iteration-6
// multi-root corner: an assign carrying a DIFFERENT root used to replace the
// global in-memory map with that root's disk map, silently dropping the
// primary root's live assignments. The fix: same-root assigns keep the
// full-map swap + activation; cross-root assigns persist to that root's
// store but never swap the in-memory map, and name the target root in the
// response.
func TestAssignCrossRootPersistsWithoutMemorySwap(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	primaryRoot = t.TempDir() // root #1: this process's enforcement root
	root2 := t.TempDir()      // root #2: a foreign root
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	// Assign to the PRIMARY root: activates in memory (same-root path).
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "local-agent",
		"role":     "developer",
		"root":     primaryRoot,
	}); err != nil {
		t.Fatalf("assign primary root: %v", err)
	}

	// Assign to the FOREIGN root: persists there, but must NOT swap the
	// in-memory map or drop the primary root's live entries.
	res, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "remote-agent",
		"role":     "admin",
		"root":     root2,
	})
	if err != nil {
		t.Fatalf("assign foreign root: %v", err)
	}
	// The response is honest about where the assignment went.
	if !strings.Contains(res, "persisted for "+root2) {
		t.Errorf("cross-root response must name the target root, got: %s", res)
	}

	// Foreign root's disk store carries the assignment.
	roles2, err := governance.LoadRBACRoles(root2)
	if err != nil {
		t.Fatalf("LoadRBACRoles(root2): %v", err)
	}
	if roles2["remote-agent"] != "admin" {
		t.Fatalf("foreign root store = %v, want remote-agent=admin", roles2)
	}

	// In-memory map is unchanged: still the primary root's entries.
	rbacMu.RLock()
	got := make(map[string]string, len(agentRoles))
	for id, r := range agentRoles {
		got[id] = r
	}
	rbacMu.RUnlock()
	if got["local-agent"] != "developer" {
		t.Fatalf("primary root entry dropped: agentRoles = %v, want local-agent=developer", got)
	}
	if _, leaked := got["remote-agent"]; leaked {
		t.Fatalf("foreign assignment leaked into the in-memory map: %v", got)
	}
	// Enforcement still reflects the primary root's map: the foreign agent
	// stays unassigned in this process (legacy trust permits, no role).
	if allowed, reason := CheckAgentTool("remote-agent", "kern_exec"); !allowed {
		t.Errorf("foreign agent must remain unassigned in this process, got denied (%s)", reason)
	}
}

// TestInitCapturesPrimaryRootFromCwd verifies init's root capture: the
// package var follows the process cwd, so an assign to any other root is
// treated as cross-root (persist-only) instead of swapping the map.
func TestInitCapturesPrimaryRootFromCwd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)                          // simulate a process started inside a project root
	primaryRoot = governance.ProcessRoot() // the exact capture init() performs
	if primaryRoot != root {
		t.Fatalf("primaryRoot = %q, want %q (process cwd)", primaryRoot, root)
	}
}

// TestCheckAgentToolOrgWins covers P13 stage 2 org-scope RBAC: with an org
// root configured and an org role assigned, the ORG role wins over the
// per-project role; with no org role the project role stands; with neither
// the legacy unassigned posture holds. Also proves org-scope assign persists
// to the org store and does NOT swap the per-project in-memory map (the
// Stage-A multi-root semantics: org enforcement is disk-rooted, project
// enforcement is memory-rooted).
func TestCheckAgentToolOrgWins(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	orgRoot := t.TempDir()
	projectRoot := t.TempDir()
	primaryRoot = projectRoot // in-package hook: project assigns activate here
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	// Project role: junior_dev (denies kern_exec).
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "dual-1",
		"role":     "junior_dev",
		"root":     projectRoot,
	}); err != nil {
		t.Fatalf("project assign: %v", err)
	}
	// Without an org role, the project role is enforced.
	if allowed, _ := CheckAgentTool("dual-1", "kern_exec"); allowed {
		t.Error("project role must deny kern_exec when no org role is assigned")
	}

	// Org role: admin. Org wins: kern_exec now allowed.
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "dual-1",
		"role":     "admin",
		"scope":    "org",
	}); err != nil {
		t.Fatalf("org assign: %v", err)
	}
	if allowed, reason := CheckAgentTool("dual-1", "kern_exec"); !allowed {
		t.Errorf("org role admin must win over project junior_dev, denied (%s)", reason)
	}

	// The org assign persisted to the org store...
	roles, err := orgapprovals.LoadOrgRBACRoles(orgRoot)
	if err != nil {
		t.Fatalf("LoadOrgRBACRoles: %v", err)
	}
	if roles["dual-1"] != "admin" {
		t.Fatalf("org store = %v, want dual-1=admin", roles)
	}
	// ...and did NOT swap the per-project in-memory map (still junior_dev).
	rbacMu.RLock()
	got := agentRoles["dual-1"]
	rbacMu.RUnlock()
	if got != "junior_dev" {
		t.Fatalf("per-project in-memory map = %q, want junior_dev (org assign must not swap it)", got)
	}
	// The per-project DISK store is untouched too.
	proj, err := governance.LoadRBACRoles(projectRoot)
	if err != nil {
		t.Fatalf("LoadRBACRoles(projectRoot): %v", err)
	}
	if proj["dual-1"] != "junior_dev" {
		t.Fatalf("project store = %v, want dual-1=junior_dev", proj)
	}

	// An agent with NO org role still falls back to its project role.
	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "proj-only",
		"role":     "junior_dev",
		"root":     projectRoot,
	}); err != nil {
		t.Fatalf("project assign 2: %v", err)
	}
	if allowed, _ := CheckAgentTool("proj-only", "kern_exec"); allowed {
		t.Error("project-only agent must be denied kern_exec (org fallback = project)")
	}
	if allowed, reason := CheckAgentTool("proj-only", "kern_compact_file"); !allowed {
		t.Errorf("project-only agent must be allowed kern_compact_file, got (%s)", reason)
	}

	// An agent with NEITHER role keeps the legacy unassigned posture.
	if allowed, reason := CheckAgentTool("unassigned-org", "kern_exec"); !allowed {
		t.Errorf("no org role and no project role: legacy permit-all must hold, got denied (%s)", reason)
	}
}

// TestCheckAgentToolOrgReviewerDeniedExecUnderDefaultDeny covers the
// default-deny × org-role interaction: an agent with an ORG reviewer role is
// read-only even under KERN_RBAC_DEFAULT_DENY — org reviewer must deny exec
// exactly like a project reviewer.
func TestCheckAgentToolOrgReviewerDeniedExecUnderDefaultDeny(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	orgRoot := t.TempDir()
	projectRoot := t.TempDir()
	primaryRoot = projectRoot
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1") // also auto-permits assign

	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "org-reviewer",
		"role":     "reviewer",
		"scope":    "org",
	}); err != nil {
		t.Fatalf("org assign reviewer: %v", err)
	}
	if allowed, reason := CheckAgentTool("org-reviewer", "kern_exec"); allowed {
		t.Errorf("org reviewer must deny kern_exec, allowed (%s)", reason)
	}
	if allowed, reason := CheckAgentTool("org-reviewer", "kern_compact_file"); !allowed {
		t.Errorf("org reviewer must allow read tools, denied (%s)", reason)
	}
	// Default-deny still applies to agents with NO role anywhere.
	if allowed, _ := CheckAgentTool("unassigned-deny", "kern_exec"); allowed {
		t.Error("default-deny: unassigned agent must be denied kern_exec")
	}
}

// TestAssignOrgScopeRequiresOrgRoot covers the org-scope assign guard: with
// no org root configured, scope=org is refused (org scope is strictly
// opt-in) and the same privilege-escalation guard as project assign applies.
func TestAssignOrgScopeRequiresOrgRoot(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	t.Setenv("KERN_ORG_ROOT", "")
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")
	primaryRoot = t.TempDir()
	_, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "nobody",
		"role":     "admin",
		"scope":    "org",
	})
	if err == nil {
		t.Fatal("org-scope assign without an org root must fail closed")
	}
	if !strings.Contains(err.Error(), governance.OrgRootEnv) {
		t.Errorf("error must name %s, got: %v", governance.OrgRootEnv, err)
	}
	// Org-scope assign still fails closed without the operator opt-in.
	os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")
	_, err = Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "nobody",
		"role":     "admin",
		"scope":    "org",
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("org-scope assign must keep the fail-closed guard, got: %v", err)
	}
}

// TestCheckAgentToolIgnoresOrgStoreWithoutOrgRoot — org scope is strictly
// opt-in: org roles persisted on disk are IGNORED when no org root is
// configured (byte-for-byte backward compatibility).
func TestCheckAgentToolIgnoresOrgStoreWithoutOrgRoot(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	t.Setenv("KERN_ORG_ROOT", "")
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")
	projectRoot := t.TempDir()
	primaryRoot = projectRoot

	// An org store exists with an admin role for the agent...
	orgRoot := t.TempDir()
	if err := orgapprovals.SaveOrgRBACRoles(orgRoot, map[string]string{"org-only": "admin"}); err != nil {
		t.Fatal(err)
	}
	// ...but KERN_ORG_ROOT is unset: the agent is unassigned (legacy trust).
	if allowed, reason := CheckAgentTool("org-only", "kern_exec"); !allowed {
		t.Errorf("without an org root the org store must be ignored (legacy permit-all), got denied (%s)", reason)
	}
}

// TestHandleEvaluateConsultsOrgRoles covers the evaluate action's org-wins
// resolution: a role-less evaluate for an agent with an org role reports the
// ORG role's verdict.
func TestHandleEvaluateConsultsOrgRoles(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	orgRoot := t.TempDir()
	primaryRoot = t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	if _, err := Handle(context.Background(), map[string]any{
		"action":   "assign",
		"agent_id": "eval-org",
		"role":     "reviewer",
		"scope":    "org",
	}); err != nil {
		t.Fatalf("org assign: %v", err)
	}
	res, err := Handle(context.Background(), map[string]any{
		"action":   "evaluate",
		"agent_id": "eval-org",
		"tool":     "kern_exec",
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !strings.Contains(res, "DENIED") {
		t.Errorf("evaluate must resolve the org reviewer role and deny kern_exec, got: %s", res)
	}
	if !strings.Contains(res, "reviewer") {
		t.Errorf("evaluate must report the org role name reviewer, got: %s", res)
	}
}

// TestCachedOrgRolesReloadsOnChange covers the finding-5 mtime cache: a
// missing store reads as empty and a store created afterwards is picked up;
// an out-of-band change (mtime/size change) reloads; an unchanged store
// serves the cached map; no org root → empty map with zero disk access.
func TestCachedOrgRolesReloadsOnChange(t *testing.T) {
	orgRoot := t.TempDir()
	// No org root → empty map, no disk access.
	if roles, err := orgapprovals.CachedOrgRolesFor(""); err != nil || len(roles) != 0 {
		t.Fatalf("orgapprovals.CachedOrgRolesFor(\"\") = %v, %v; want empty", roles, err)
	}
	// Missing store → empty map (and the cache resets so a later-created
	// store is picked up).
	if roles, err := orgapprovals.CachedOrgRolesFor(orgRoot); err != nil || len(roles) != 0 {
		t.Fatalf("orgapprovals.CachedOrgRolesFor(missing) = %v, %v; want empty", roles, err)
	}
	// Seed the store; the next read must reload it (created after the miss).
	if err := orgapprovals.SaveOrgRBACRoles(orgRoot, map[string]string{"a1": "admin"}); err != nil {
		t.Fatal(err)
	}
	roles, err := orgapprovals.CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "admin" {
		t.Fatalf("orgapprovals.CachedOrgRolesFor after seed = %v, %v; want a1=admin", roles, err)
	}
	// An out-of-band change (different content ⇒ different size) must be
	// picked up: org-role changes take effect within one mtime granularity.
	if err := orgapprovals.SaveOrgRBACRoles(orgRoot, map[string]string{"a1": "reviewer"}); err != nil {
		t.Fatal(err)
	}
	roles, err = orgapprovals.CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "reviewer" {
		t.Fatalf("orgapprovals.CachedOrgRolesFor must reload on change, got %v, %v; want a1=reviewer", roles, err)
	}
	// Unchanged store → the cached map is served again, still fresh.
	roles, err = orgapprovals.CachedOrgRolesFor(orgRoot)
	if err != nil || roles["a1"] != "reviewer" {
		t.Fatalf("cached read = %v, %v; want a1=reviewer", roles, err)
	}
}

// TestOrgAssignInvalidatesRoleCache covers the write-side cache invalidation
// (R3): AssignOrgRole — now in orgapprovals, where the org-role cache lives
// with the store it caches — invalidates the cache directly after its atomic
// save, so the VERY NEXT CheckAgentTool sees the new role with zero
// disk-read staleness. The mtime+size check is made blind on purpose —
// os.Chtimes freezes the store's mtime to the cached value and the two roles
// are equal-length names (same file size) — so an mtime/size-only cache
// would serve the stale role; only the write-side invalidation makes the
// next resolve reflect the change. (The cache-state assertions this test
// used to make against rbac's private vars now live in orgapprovals'
// TestAssignOrgRoleInvalidatesCache, next to the cache.)
func TestOrgAssignInvalidatesRoleCache(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	orgRoot := t.TempDir()
	projectRoot := t.TempDir()
	primaryRoot = projectRoot
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1")

	// 1. Org assign "architect" (denies kern_exec) — persists to the store.
	if _, err := Handle(context.Background(), map[string]any{
		"action": "assign", "agent_id": "role-swap", "role": "architect", "scope": "org",
	}); err != nil {
		t.Fatalf("org assign architect: %v", err)
	}
	// 2. First resolve populates the mtime+size cache; architect denies exec.
	if allowed, _ := CheckAgentTool("role-swap", "kern_exec"); allowed {
		t.Fatal("architect must deny kern_exec before the swap")
	}
	path := orgapprovals.OrgRBACPath(orgRoot)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat org rbac store: %v", err)
	}

	// 3. Org assign "developer" (equal-length name => same file size) to the
	//    same agent — the write-side invalidation must fire here (it runs
	//    inside AssignOrgRole, after the atomic save).
	if _, err := Handle(context.Background(), map[string]any{
		"action": "assign", "agent_id": "role-swap", "role": "developer", "scope": "org",
	}); err != nil {
		t.Fatalf("org assign developer: %v", err)
	}

	// 4. Freeze the store's mtime to the value the cache recorded: same
	//    mtime and same size means the mtime+size check alone cannot detect
	//    the swap — only the write-side invalidation can.
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatalf("chtimes freeze: %v", err)
	}

	// 5. The VERY NEXT resolve must see the new role (developer allows
	//    kern_exec) with zero disk-read staleness.
	if allowed, reason := CheckAgentTool("role-swap", "kern_exec"); !allowed {
		t.Fatalf("next CheckAgentTool must see the swapped role, got denied (%s)", reason)
	}
}

// TestDirectOrgApprovalsAssignVisibleToNextCheck is the R3 enterprise-REST
// regression: the enterprise REST RegisterAgent/role-bind path calls
// orgapprovals.AssignOrgRole DIRECTLY (no MCP Handle, no rbac observer
// plumbing). That direct write must be visible to the very next
// rbac.CheckAgentTool with the store's mtime frozen (os.Chtimes) and an
// equal-length role name (same file size) — only AssignOrgRole's built-in
// cache invalidation makes this work.
func TestDirectOrgApprovalsAssignVisibleToNextCheck(t *testing.T) {
	ResetMemory()
	defer ResetMemory()
	orgRoot := t.TempDir()
	projectRoot := t.TempDir()
	primaryRoot = projectRoot
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "") // legacy trust for the unassigned project role

	// 1. Enterprise-REST-style direct write: architect (denies kern_exec).
	if err := orgapprovals.AssignOrgRole(orgRoot, "enterprise-agent", "architect"); err != nil {
		t.Fatalf("direct AssignOrgRole architect: %v", err)
	}
	// 2. First resolve warms the cache; architect denies exec.
	if allowed, _ := CheckAgentTool("enterprise-agent", "kern_exec"); allowed {
		t.Fatal("architect must deny kern_exec before the swap")
	}
	path := orgapprovals.OrgRBACPath(orgRoot)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat org rbac store: %v", err)
	}

	// 3. Direct write again, equal-length role name ("developer" == 9 chars
	//    like "architect" => same file size) — simulating the enterprise
	//    REST role-bind path; no rbac code runs here.
	if err := orgapprovals.AssignOrgRole(orgRoot, "enterprise-agent", "developer"); err != nil {
		t.Fatalf("direct AssignOrgRole developer: %v", err)
	}

	// 4. Freeze the store's mtime to the cached value: same mtime + same
	//    size means the mtime+size check alone cannot detect the swap.
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatalf("chtimes freeze: %v", err)
	}

	// 5. The VERY NEXT CheckAgentTool must see developer (allows kern_exec) —
	//    the direct write's invalidation is visible with zero staleness.
	if allowed, reason := CheckAgentTool("enterprise-agent", "kern_exec"); !allowed {
		t.Fatalf("next CheckAgentTool must see the direct-write role swap, got denied (%s)", reason)
	}
}
