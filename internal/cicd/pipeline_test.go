package cicd

import (
	"testing"
)

func TestPipelineReadOnlyAnalyzePlan(t *testing.T) {
	// Read-only analyze+plan should work without governance gates.
	p, err := New("../..")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := p.Run(Trigger{
		Source: "test",
		Intent: "NewServer",
	})
	if res.Error != "" {
		t.Errorf("unexpected error: %s", res.Error)
	}
	if res.Phase != "planned" {
		t.Errorf("phase = %q, want 'planned'", res.Phase)
	}
	if res.Task == nil {
		t.Error("task should not be nil")
	}
}

func TestPipelinePatchExecutionGovernanceGate(t *testing.T) {
	// Without KERN_ALLOW_EXEC, execution should be denied by governance.
	p, err := New("../..")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	patch := `diff --git a/_test_cicd.go b/_test_cicd.go
new file mode 100644
--- /dev/null
+++ b/_test_cicd.go
@@ -0,0 +1,3 @@
+// Test file for CI/CD pipeline test.
+package main
+
`
	res := p.Run(Trigger{
		Source: "test",
		Intent: "test change",
		Patch:  patch,
	})
	if res.GateResult != "denied" {
		t.Errorf("gate result = %q, want 'denied' (KERN_ALLOW_EXEC not set)", res.GateResult)
	}
	if res.Approved {
		t.Error("should not be approved without KERN_ALLOW_EXEC")
	}
}

func TestPipelineExecutionGateApproved(t *testing.T) {
	// With KERN_ALLOW_EXEC=1, the governance gate must approve. We assert only
	// the gate wiring here — the actual worktree build/verify may fail on the
	// sandbox 100MiB cap, which is unrelated to the CI/CD governance wiring.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	p, err := New("../..")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	patch := `diff --git a/_test_cicd.go b/_test_cicd.go
new file mode 100644
--- /dev/null
+++ b/_test_cicd.go
@@ -0,0 +1,3 @@
+// Test file for CI/CD pipeline test.
+package main
+
`
	res := p.Run(Trigger{
		Source: "test",
		Intent: "test change",
		Patch:  patch,
	})
	if res.GateResult != "approved" {
		t.Errorf("gate result = %q, want 'approved'", res.GateResult)
	}
	if !res.Approved {
		t.Error("should be approved with KERN_ALLOW_EXEC=1")
	}
}

func TestParseRepo(t *testing.T) {
	cases := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"JayveerPrajapati/kern", "JayveerPrajapati", "kern"},
		{"octocat/Hello-World", "octocat", "Hello-World"},
		{"", "", ""},
		{"invalid", "", ""},
		{"a/b/c", "a", "b"}, // only first two segments
	}
	for _, tc := range cases {
		owner, repo := parseRepo(tc.input)
		if owner != tc.wantOwner || repo != tc.wantRepo {
			t.Errorf("parseRepo(%q) = (%q, %q), want (%q, %q)", tc.input, owner, repo, tc.wantOwner, tc.wantRepo)
		}
	}
}