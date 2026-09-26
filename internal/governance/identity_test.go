package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewAgent(t *testing.T) {
	a := NewAgent("coder-1", "Coder", "coder", []Permission{
		{Resource: "source", Action: "write"},
		{Resource: "tests", Action: "write"},
	})
	if a.ID != "coder-1" {
		t.Fatalf("ID = %q, want coder-1", a.ID)
	}
	if a.Name != "Coder" || a.Type != "coder" {
		t.Errorf("Name/Type = %q/%q, want Coder/coder", a.Name, a.Type)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set to current time")
	}
	if len(a.Permissions) != 2 {
		t.Errorf("Permissions len = %d, want 2", len(a.Permissions))
	}
}

func TestNewAgentNilPermissions(t *testing.T) {
	a := NewAgent("empty", "Empty", "coder", nil)
	if a == nil {
		t.Fatal("NewAgent should not return nil")
	}
	if a.Permissions != nil {
		t.Errorf("Permissions = %v, want nil", a.Permissions)
	}
}

func TestCan(t *testing.T) {
	a := NewAgent("coder-1", "Alice", "coder", []Permission{
		{Resource: "source", Action: "write"},
		{Resource: "tests", Action: "read"},
	})
	cases := []struct {
		name          string
		resource, act string
		want          bool
	}{
		{"exact grant", "source", "write", true},
		{"wrong action", "source", "read", false},
		{"second grant", "tests", "read", true},
		{"wrong resource", "tests", "write", false},
		{"unlisted resource", "docs", "write", false},
		{"empty pair", "", "", false},
		{"case sensitive", "SOURCE", "write", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := a.Can(c.resource, c.act); got != c.want {
				t.Errorf("Can(%q,%q) = %v, want %v", c.resource, c.act, got, c.want)
			}
		})
	}
}

func TestCanEmptyPermissions(t *testing.T) {
	a := NewAgent("bare", "Bare", "coder", nil)
	if a.Can("source", "write") {
		t.Error("Can with no permissions should be false (fail closed)")
	}
}

func TestHasPermissionAlias(t *testing.T) {
	a := NewAgent("x", "X", "coder", []Permission{{Resource: "source", Action: "write"}})
	if !a.HasPermission("source", "write") {
		t.Error("HasPermission should match a granted permission")
	}
	if a.HasPermission("source", "read") {
		t.Error("HasPermission should not match an ungranted permission")
	}
}

func TestRegisterAndGetAgent(t *testing.T) {
	a := NewAgent("reg-1", "R", "reviewer", nil)
	if err := RegisterAgent(a); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	got, err := GetAgent("reg-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.Name != "R" {
		t.Errorf("Name = %q, want R", got.Name)
	}
}

func TestRegisterAgentOverwrite(t *testing.T) {
	// Re-registering an existing ID should update the stored identity.
	first := NewAgent("dup", "First", "coder", nil)
	if err := RegisterAgent(first); err != nil {
		t.Fatalf("first RegisterAgent: %v", err)
	}
	second := NewAgent("dup", "Second", "coder", nil)
	if err := RegisterAgent(second); err != nil {
		t.Fatalf("second RegisterAgent: %v", err)
	}
	got, err := GetAgent("dup")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.Name != "Second" {
		t.Errorf("Name = %q, want Second (overwritten)", got.Name)
	}
}

func TestRegisterAgentRejectsInvalid(t *testing.T) {
	if err := RegisterAgent(nil); err == nil {
		t.Error("RegisterAgent(nil) should error")
	}
	if err := RegisterAgent(NewAgent("", "no-id", "coder", nil)); err == nil {
		t.Error("RegisterAgent with empty ID should error")
	}
}

func TestGetAgentUnknown(t *testing.T) {
	if _, err := GetAgent("does-not-exist"); err == nil {
		t.Error("GetAgent for unknown ID should error (fail closed)")
	}
}

// TestSaveLoadAgentsRoundTrip verifies an agent registered in one process
// (registry instance) is visible to a later process via the store — the
// `kern org agents register` -> `kern authorize-context` contract.
func TestSaveLoadAgentsRoundTrip(t *testing.T) {
	root := t.TempDir()
	a := NewAgent("qa-agent", "QA Agent", "tester", []Permission{{Resource: "context", Action: "read"}})
	if err := RegisterAgent(a); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// restore pristine registry for other tests
		agentRegistryMu.Lock()
		delete(agentRegistry, "qa-agent")
		agentRegistryMu.Unlock()
	}()
	if err := SaveAgents(root); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}
	// Simulate a fresh process: wipe the in-memory registry, then load.
	agentRegistryMu.Lock()
	agentRegistry = map[string]*AgentIdentity{}
	agentRegistryMu.Unlock()
	if err := LoadAgents(root); err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	got, err := GetAgent("qa-agent")
	if err != nil {
		t.Fatalf("GetAgent after LoadAgents: %v", err)
	}
	if got.Name != "QA Agent" || len(got.Permissions) != 1 || got.Permissions[0].Resource != "context" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestLoadAgentsMissingStoreIsNoop ensures a fresh project (no agents.json)
// leaves the registry untouched rather than erroring — lookups still fail
// closed with "unknown agent".
func TestLoadAgentsMissingStoreIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := LoadAgents(root); err != nil {
		t.Fatalf("LoadAgents on missing store should be a no-op, got %v", err)
	}
	if _, err := GetAgent("nobody"); err == nil {
		t.Error("GetAgent for unknown ID should still fail closed")
	}
}

// TestPersistAgentConcurrentNoLoss hammers PersistAgent from many goroutines
// at once. The old plain os.WriteFile read-modify-write interleaved and
// silently lost other agents; the locked flock + atomic-rename critical
// section must preserve every registration.
func TestPersistAgentConcurrentNoLoss(t *testing.T) {
	root := t.TempDir()
	const agents = 20
	var wg sync.WaitGroup
	errs := make(chan error, agents)
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%02d", i)
			a := NewAgent(id, "Agent "+id, "tester", []Permission{{Resource: "context", Action: "read"}})
			if err := PersistAgent(root, a); err != nil {
				errs <- fmt.Errorf("persist %s: %w", id, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	persisted := PersistedAgents(root)
	if len(persisted) != agents {
		t.Fatalf("PersistedAgents = %d, want %d (concurrent updates lost)", len(persisted), agents)
	}
	ids := map[string]bool{}
	for _, a := range persisted {
		ids[a.ID] = true
	}
	for i := 0; i < agents; i++ {
		if !ids[fmt.Sprintf("agent-%02d", i)] {
			t.Errorf("agent-%02d missing from store", i)
		}
	}
}

// TestPersistAgentCorruptStoreFailsClosed: a corrupt agents.json must fail
// closed (registration errors) instead of being overwritten — the old code
// swallowed the unmarshal error and rewrote the file, destroying every other
// agent identity it contained.
func TestPersistAgentCorruptStoreFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := agentStorePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PersistAgent(root, NewAgent("a1", "A", "tester", nil)); err == nil {
		t.Fatal("PersistAgent on a corrupt store should fail closed, got nil")
	}
	// The corrupt content must be left untouched, not overwritten.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Errorf("corrupt store was overwritten: %q", data)
	}

	// A healthy store still works after the fix.
	root2 := t.TempDir()
	if err := PersistAgent(root2, NewAgent("b1", "B", "tester", nil)); err != nil {
		t.Fatalf("PersistAgent on healthy store: %v", err)
	}
	if got := PersistedAgents(root2); len(got) != 1 {
		t.Fatalf("PersistedAgents = %d, want 1", len(got))
	}
}
