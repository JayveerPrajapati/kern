package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

func TestValidScope(t *testing.T) {
	cases := []struct {
		scope string
		want  bool
	}{
		{"db-models", true},
		{"db_models", true},
		{"Db.Models1", true},
		{"", false},
		{".hidden", false},
		{"..", false},
		{"a/b", false},
		{"a b", false},
		{"a;rm", false},
		{"../../../../tmp/pwn", false},
		{"日本語", false},
	}
	for _, c := range cases {
		if got := validScope(c.scope); got != c.want {
			t.Errorf("validScope(%q) = %v, want %v", c.scope, got, c.want)
		}
	}
}

// testHooks builds a Hooks with a fresh lock registry (non-nil map, like the
// server's) and a real index loader over root.
func testHooks(t *testing.T, root string) Hooks {
	t.Helper()
	return Hooks{
		Mu:        &sync.Mutex{},
		Locks:     map[string]*lock.Lock{},
		LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return index.Build(r) },
		Guide:     func() string { return "guide-text" },
		StampGoverned: func(ctx context.Context, ix *index.Index, policySource string, proof governance.AuthorizationProof, symbols []provenance.SymbolProvenance) {
		},
	}
}

func TestLockUnlockRoundtrip(t *testing.T) {
	root := t.TempDir()
	h := testHooks(t, root)

	out, err := Lock(context.Background(), h, map[string]any{"scope": "db-models", "root": root})
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !strings.Contains(out, "lock acquired: db-models") {
		t.Fatalf("Lock output = %q", out)
	}

	// The lock is registered on the shared map. A second Acquire on the same
	// scope contends with the held OS-native lock, so it reports "held".
	if _, err := Lock(context.Background(), h, map[string]any{"scope": "db-models", "root": root}); err == nil ||
		!strings.Contains(err.Error(), "is held") {
		t.Fatalf("re-Lock err = %v, want held", err)
	}

	// LockStatus lists the held lock.
	sts, err := LockStatus(context.Background(), h, map[string]any{"root": root})
	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if !strings.Contains(sts, "db-models HELD") {
		t.Fatalf("LockStatus output = %q", sts)
	}

	// Unlock releases and removes it.
	out, err = Unlock(context.Background(), h, map[string]any{"scope": "db-models", "root": root})
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if out != "lock released: db-models" {
		t.Fatalf("Unlock output = %q", out)
	}

	// A second unlock reports not-held.
	if _, err := Unlock(context.Background(), h, map[string]any{"scope": "db-models", "root": root}); err == nil ||
		!strings.Contains(err.Error(), "not held by this server") {
		t.Fatalf("second Unlock err = %v, want not-held", err)
	}
}

func TestLockErrors(t *testing.T) {
	root := t.TempDir()
	h := testHooks(t, root)

	if _, err := Lock(context.Background(), h, map[string]any{"root": root}); err == nil ||
		!strings.Contains(err.Error(), "scope is required") {
		t.Fatalf("missing scope err = %v", err)
	}
	if _, err := Lock(context.Background(), h, map[string]any{"scope": "a/b", "root": root}); err == nil ||
		!strings.Contains(err.Error(), "invalid lock scope") {
		t.Fatalf("invalid scope err = %v", err)
	}
}

func TestLockStatusEmpty(t *testing.T) {
	root := t.TempDir()
	h := testHooks(t, root)
	sts, err := LockStatus(context.Background(), h, map[string]any{"root": root})
	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if sts != "no locks in workspace" {
		t.Fatalf("LockStatus = %q, want no-locks message", sts)
	}
}

func TestUsageGuide(t *testing.T) {
	h := testHooks(t, t.TempDir())
	out, err := UsageGuide(context.Background(), h, map[string]any{})
	if err != nil {
		t.Fatalf("UsageGuide: %v", err)
	}
	if out != "guide-text" {
		t.Fatalf("UsageGuide = %q, want hook output", out)
	}
}

// TestRenamePreview pins the preview path: no apply, no mutation, rendered
// plan names the new symbol.
func TestRenamePreview(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("a.go", "package p\n\nfunc Hub() int { return 0 }\n")
	h := testHooks(t, root)

	out, err := Rename(context.Background(), h, map[string]any{"root": root, "symbol": "Hub", "new_name": "Hub2"})
	if err != nil {
		t.Fatalf("Rename preview: %v", err)
	}
	if !strings.Contains(out, "Hub2") {
		t.Fatalf("Rename preview output missing new name: %s", out)
	}
	body, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if strings.Contains(string(body), "Hub2") {
		t.Fatal("preview must not mutate the tree")
	}
}
