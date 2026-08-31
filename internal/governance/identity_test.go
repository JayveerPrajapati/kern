package governance

import (
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
