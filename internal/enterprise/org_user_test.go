package enterprise

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestUserRegistryRoundTrip drives the full registry lifecycle:
// add -> list -> role -> disable -> audit, including the actor-aware
// audit trail (By records the acting identity).
func TestUserRegistryRoundTrip(t *testing.T) {
	s := mustNew(t)
	if err := s.AddUserBy("alice", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("AddUserBy: %v", err)
	}
	if err := s.AddUserBy("bob", "developer", "alice"); err != nil {
		t.Fatalf("AddUserBy: %v", err)
	}
	users := s.ListUsers()
	if len(users) != 2 {
		t.Fatalf("ListUsers = %d users, want 2", len(users))
	}
	if users[0].ID != "alice" || users[0].Role != "org-admin" || !users[0].Enabled {
		t.Errorf("users[0] = %+v, want alice/org-admin/enabled", users[0])
	}
	if users[1].ID != "bob" || users[1].Role != "developer" {
		t.Errorf("users[1] = %+v, want bob/developer", users[1])
	}
	if users[0].CreatedAt.IsZero() || users[0].UpdatedAt.IsZero() {
		t.Error("timestamps should be set on creation")
	}
	// UserRole lookup (the RBAC read path).
	if role, ok := s.UserRole("alice"); !ok || role != "org-admin" {
		t.Errorf("UserRole(alice) = (%q, %v), want (org-admin, true)", role, ok)
	}
	if role, ok := s.UserRole("nobody"); ok {
		t.Errorf("UserRole(nobody) = (%q, true), want (\"\", false)", role)
	}
	// SetRole.
	if err := s.SetRoleBy("bob", "org-member", "alice"); err != nil {
		t.Fatalf("SetRoleBy: %v", err)
	}
	if role, _ := s.UserRole("bob"); role != "org-member" {
		t.Errorf("UserRole(bob) after SetRoleBy = %q, want org-member", role)
	}
	// DisableUser.
	if err := s.DisableUserBy("bob", "alice"); err != nil {
		t.Fatalf("DisableUserBy: %v", err)
	}
	if u, _ := s.UserRole("bob"); u != "org-member" {
		t.Errorf("bob's role should survive disable")
	}
	found := false
	for _, u := range s.ListUsers() {
		if u.ID == "bob" && u.Enabled {
			found = true
		}
	}
	if found {
		t.Error("bob should be disabled")
	}
	// Audit trail: bob has add/role/disable entries with the actors recorded.
	trail := s.UserAudit("bob")
	if len(trail) != 3 {
		t.Fatalf("UserAudit(bob) = %d entries, want 3", len(trail))
	}
	if trail[0].Action != "user-add" || trail[0].By != "alice" {
		t.Errorf("trail[0] = %+v, want user-add by alice", trail[0])
	}
	if trail[1].Action != "user-role" || trail[1].By != "alice" {
		t.Errorf("trail[1] = %+v, want user-role by alice", trail[1])
	}
	if trail[2].Action != "user-disable" || trail[2].By != "alice" {
		t.Errorf("trail[2] = %+v, want user-disable by alice", trail[2])
	}
	if trail[0].At.IsZero() {
		t.Error("audit entries must carry a timestamp")
	}
	// Unknown user: empty (non-nil) trail.
	if got := s.UserAudit("nobody"); got == nil || len(got) != 0 {
		t.Errorf("UserAudit(nobody) = %v, want empty non-nil slice", got)
	}
}

// TestUserRegistryDuplicateErrors pins the duplicate-id contract: a second
// AddUser with the same ID fails and does not clobber the first user.
func TestUserRegistryDuplicateErrors(t *testing.T) {
	s := mustNew(t)
	if err := s.AddUser("alice", "org-admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := s.AddUser("alice", "org-member"); err == nil {
		t.Fatal("duplicate AddUser: want error, got nil")
	}
	if role, _ := s.UserRole("alice"); role != "org-admin" {
		t.Errorf("first registration clobbered: role = %q, want org-admin", role)
	}
	// Validation errors: empty id / role.
	if err := s.AddUser("", "org-admin"); err == nil {
		t.Error("empty id: want error")
	}
	if err := s.AddUser("carol", ""); err == nil {
		t.Error("empty role: want error")
	}
	// Unknown-user errors for role/disable.
	if err := s.SetRole("nobody", "org-member"); err == nil {
		t.Error("SetRole(unknown): want error")
	}
	if err := s.DisableUser("nobody"); err == nil {
		t.Error("DisableUser(unknown): want error")
	}
}

// TestUserRegistryPersistenceAtomicWrites pins the JSON persistence: with a
// shared org store attached, mutations land as <dir>/users/<id>.json and
// <dir>/user-audit/<id>.json (atomic temp-file writes), and a fresh server
// over the same store replays users + audit trails on first access.
func TestUserRegistryPersistenceAtomicWrites(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	s := mustNew(t).WithStore(store)
	if err := s.AddUserBy("alice", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("AddUserBy: %v", err)
	}
	if err := s.SetRoleBy("alice", "org-member", "alice"); err != nil {
		t.Fatalf("SetRoleBy: %v", err)
	}
	// Both the user record and the audit trail must be on disk as JSON.
	userPath := filepath.Join(dir, "user-alice.json")
	auditPath := filepath.Join(dir, "user-audit-alice.json")
	for _, p := range []string{userPath, auditPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected persisted file %s: %v", p, err)
		}
	}
	// Replay: a fresh server with the same store recovers the user and the
	// audit trail (lazy load on first access).
	replayed := mustNew(t).WithStore(store)
	if role, ok := replayed.UserRole("alice"); !ok || role != "org-member" {
		t.Errorf("replayed UserRole(alice) = (%q, %v), want (org-member, true)", role, ok)
	}
	if users := replayed.ListUsers(); len(users) != 1 {
		t.Fatalf("replayed ListUsers = %d, want 1", len(users))
	}
	if trail := replayed.UserAudit("alice"); len(trail) != 2 {
		t.Fatalf("replayed UserAudit(alice) = %d entries, want 2 (add + role)", len(trail))
	}
}

// TestUserRegistryAbsentStoreSafety pins the local-mode contract: without a
// store the registry is fully in-memory and never touches the filesystem, and
// a disabled profile (basic) degrades to errors/nil instead of panicking.
func TestUserRegistryAbsentStoreSafety(t *testing.T) {
	dir := t.TempDir()
	s := mustNew(t) // no store
	if err := s.AddUser("alice", "org-admin"); err != nil {
		t.Fatalf("AddUser without store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "user-alice.json")); !os.IsNotExist(err) {
		t.Errorf("no store: expected no filesystem writes, found %v", err)
	}
	if users := s.ListUsers(); len(users) != 1 {
		t.Errorf("ListUsers without store = %d, want 1", len(users))
	}
	// Basic profile disables the registry: mutations error, reads degrade.
	basic := mustNew(t).WithProfile(ProfileBasic)
	if err := basic.AddUser("bob", "org-admin"); err == nil {
		t.Error("basic profile AddUser: want 'user registry disabled' error")
	}
	if users := basic.ListUsers(); users != nil {
		t.Errorf("basic profile ListUsers = %v, want nil", users)
	}
	if _, ok := basic.UserRole("bob"); ok {
		t.Error("basic profile UserRole: want (\"\", false)")
	}
}
