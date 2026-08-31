package governance

import (
	"testing"
)

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
