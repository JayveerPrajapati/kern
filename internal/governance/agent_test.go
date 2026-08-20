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
		t.Errorf("Name/Type = %q/%q", a.Name, a.Type)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if len(a.Permissions) != 2 {
		t.Errorf("Permissions len = %d, want 2", len(a.Permissions))
	}
}

func TestAgentCan(t *testing.T) {
	a := NewAgent("coder-1", "Alice", "coder", []Permission{
		{Resource: "source", Action: "write"},
		{Resource: "tests", Action: "read"},
	})
	cases := []struct {
		resource, action string
		want             bool
	}{
		{"source", "write", true},
		{"source", "read", false},
		{"tests", "read", true},
		{"tests", "write", false},
		{"docs", "write", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := a.Can(c.resource, c.action); got != c.want {
			t.Errorf("Can(%q,%q) = %v, want %v", c.resource, c.action, got, c.want)
		}
	}
}

func TestAgentHasPermissionAlias(t *testing.T) {
	a := NewAgent("x", "X", "coder", []Permission{{Resource: "source", Action: "write"}})
	if !a.HasPermission("source", "write") {
		t.Error("HasPermission should match granted permission")
	}
	if a.HasPermission("source", "read") {
		t.Error("HasPermission should not match ungranted permission")
	}
}

func TestRegisterGetAgent(t *testing.T) {
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
		t.Error("GetAgent for unknown ID should error")
	}
}
