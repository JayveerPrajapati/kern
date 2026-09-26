package governance

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRBACRolesStoreRoundTrip covers the persistence half of RBAC role
// assignments (audit iteration-2 finding 8): a missing store loads as an
// empty map, a corrupt store FAILS CLOSED with an error (never a partial
// privilege set), saves are atomic and owner-only, and nil/empty maps round
// trip.
func TestRBACRolesStoreRoundTrip(t *testing.T) {
	root := t.TempDir()

	// Missing store: empty map, no error.
	roles, err := LoadRBACRoles(root)
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
	bad := filepath.Join(root, ".kern", "rbac.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRBACRoles(root); err == nil {
		t.Fatal("corrupt store must fail closed with an error")
	}

	// Round trip after removing the corrupt file.
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := SaveRBACRoles(root, map[string]string{"agent-a": "admin"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	roles, err = LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("load after save: %v", err)
	}
	if roles["agent-a"] != "admin" {
		t.Fatalf("round trip = %v, want agent-a=admin", roles)
	}

	// Owner-only (0o600): assignments reveal who holds elevated roles.
	info, err := os.Stat(bad)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rbac.json perms = %v, want 0600", info.Mode().Perm())
	}

	// Nil/empty maps round trip as an empty map.
	if err := SaveRBACRoles(root, nil); err != nil {
		t.Fatalf("save nil: %v", err)
	}
	roles, err = LoadRBACRoles(root)
	if err != nil {
		t.Fatalf("load after nil save: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("nil save loaded %v, want empty", roles)
	}
}

// TestSameRoot covers the root comparison RBAC assign uses to separate
// same-root (activate in memory) from cross-root (persist-only) assigns:
// identical, cleaned (trailing slash), and symlink-resolved paths match;
// empty roots and distinct directories never match.
func TestSameRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()

	if !SameRoot(root, root) {
		t.Fatalf("SameRoot(%q, %q) = false, want true (identical)", root, root)
	}
	if !SameRoot(root+"/", root) {
		t.Fatalf("SameRoot(%q, %q) = false, want true (cleaned)", root+"/", root)
	}
	if SameRoot(root, other) {
		t.Fatalf("SameRoot(%q, %q) = true, want false (distinct)", root, other)
	}
	if SameRoot("", root) || SameRoot(root, "") {
		t.Fatal("empty root must never match")
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, link); err == nil {
		if !SameRoot(link, root) {
			t.Fatalf("SameRoot(%q, %q) = false, want true (symlink resolves)", link, root)
		}
	}
}
