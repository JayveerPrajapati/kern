package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanBlockedByConstitution verifies the exit gate: a mandatory
// constitution rule (MUST/MUST_NOT) blocks a plan BEFORE execution. A
// constitution whose architecture rule forbids a dependency the plan produces
// must cause TaskService.Plan to fail the task instead of completing it.
func TestPlanBlockedByConstitution(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module constfixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string {\n\treturn tenantCache()\n}\n\n// tenantCache is a caching helper.\nfunc tenantCache() string { return \"t\" }\n")
	// Constitution: payments may not depend on tenant_cache.
	kernDir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `rules:
  - id: no-tenant-cache-deps
    type: MUST_NOT
    category: architecture
    description: "no code may depend on tenantCache"
    cannot_depend_on:
      - tenantCache
`
	if err := os.WriteFile(filepath.Join(kernDir, "constitution.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, _, err := ts.Plan("NewServer")
	if err == nil {
		t.Fatal("Plan succeeded despite blocking constitution rule, want error")
	}
	if !strings.Contains(err.Error(), "plan blocked by constitution") {
		t.Errorf("error = %q, want constitution block", err)
	}
	if task.State != "FAILED" {
		t.Errorf("task state = %q, want FAILED (plan must not complete)", task.State)
	}
}

// TestPlanPassesWithoutConstitution verifies a project WITHOUT a constitution
// file is unaffected (backward compatible): the plan completes normally.
func TestPlanPassesWithoutConstitution(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module noconst\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, _, err := ts.Plan("NewServer")
	if err != nil {
		t.Fatalf("Plan without constitution should pass: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Errorf("task state = %q, want COMPLETED", task.State)
	}
}
