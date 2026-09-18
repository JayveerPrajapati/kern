package main

import (
	"strings"
	"testing"
)

// TestArtifactsTextListAll locks the all-artifacts text branch: a one-line
// summary with kind and task fields per artifact.
func TestArtifactsTextListAll(t *testing.T) {
	root := artifactFixture(t)
	out := captureStdout(t, func() { runArtifacts([]string{"--root", root}) })
	for _, want := range []string{"artifacts (1):", "kind=plan", "task=t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}
}

// TestArtifactsTextForTask locks the per-task text branch: the block render
// with every field (kind, created_by, parent, scope, provenance, created_at).
func TestArtifactsTextForTask(t *testing.T) {
	root := artifactFixture(t)
	out := captureStdout(t, func() { runArtifacts([]string{"--root", root, "t1"}) })
	for _, want := range []string{
		"artifacts for task t1 (1):",
		"kind: plan",
		"created_by: test-agent",
		"parent: (root)",
		"scope: scope-x",
		"provenance: test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task list missing %q:\n%s", want, out)
		}
	}
}

// TestArtifactsEmpty locks both empty branches: no artifacts at all, and no
// artifacts for an unknown task.
func TestArtifactsEmpty(t *testing.T) {
	root := newRoot(t)
	out := captureStdout(t, func() { runArtifacts([]string{"--root", root}) })
	if !strings.Contains(out, "no artifacts") {
		t.Errorf("expected empty listing, got %q", out)
	}
	out = captureStdout(t, func() { runArtifacts([]string{"--root", root, "t9"}) })
	if !strings.Contains(out, "no artifacts for task t9") {
		t.Errorf("expected per-task empty listing, got %q", out)
	}
}
