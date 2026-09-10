package main

import (
	"encoding/json"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// artifactFixture seeds a project root with one artifact and returns the root.
// It mirrors the command's own construction (app.New + TaskService) so the
// seeded artifact is visible to runArtifacts.
func artifactFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	a := domain.NewArtifact(domain.ArtifactPlan, "t1", "file:///tmp/plan.md")
	a.CreatedBy = "test-agent"
	a.Scope = "scope-x"
	a.Provenance = "test"
	a.Status = "draft"
	if _, err := ts.Artifacts().Save(a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return root
}

// TestArtifactsJSONFlag locks the --json output of `kern artifacts`: it must
// emit a JSON array of artifacts (all-artifacts and per-task branches) instead
// of the text table.
func TestArtifactsJSONFlag(t *testing.T) {
	root := artifactFixture(t)

	out := captureStdout(t, func() { runArtifacts([]string{"--root", root, "--json"}) })
	var arts []map[string]any
	if err := json.Unmarshal([]byte(out), &arts); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %v", len(arts), arts)
	}
	if arts[0]["Kind"] != "plan" {
		t.Fatalf("expected Kind=plan, got %v", arts[0]["Kind"])
	}
	if arts[0]["TaskID"] != "t1" {
		t.Fatalf("expected TaskID=t1, got %v", arts[0]["TaskID"])
	}
	if arts[0]["CreatedBy"] != "test-agent" {
		t.Fatalf("expected CreatedBy=test-agent, got %v", arts[0]["CreatedBy"])
	}

	// Task-scoped branch: `kern artifacts --json <taskID>`.
	out = captureStdout(t, func() { runArtifacts([]string{"--root", root, "--json", "t1"}) })
	arts = nil
	if err := json.Unmarshal([]byte(out), &arts); err != nil {
		t.Fatalf("invalid JSON for task branch: %v\n%s", err, out)
	}
	if len(arts) != 1 || arts[0]["TaskID"] != "t1" {
		t.Fatalf("expected 1 artifact for task t1, got %v", arts)
	}
}

// TestMemoryListJSONFlag locks the --json output of `kern memory list`: it must
// emit a JSON array of lessons (with timestamps) instead of the text lines.
func TestMemoryListJSONFlag(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := memory.Add(root, "deploy tags come from a manual release workflow"); err != nil {
		t.Fatalf("memory.Add: %v", err)
	}

	out := captureStdout(t, func() { runMemory([]string{"list", "--root", root, "--json"}) })
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 lesson, got %d: %v", len(entries), entries)
	}
	if entries[0]["text"] != "deploy tags come from a manual release workflow" {
		t.Fatalf("expected lesson text, got %v", entries[0]["text"])
	}
	if _, ok := entries[0]["time"]; !ok {
		t.Fatalf("expected time timestamp field, got %v", entries[0])
	}
}

// TestMemoryRecallJSONFlag locks the --json output of `kern memory recall`: it
// must emit a JSON array of recalled lessons instead of the text lines.
func TestMemoryRecallJSONFlag(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := memory.Add(root, "deploy tags come from a manual release workflow"); err != nil {
		t.Fatalf("memory.Add: %v", err)
	}

	out := captureStdout(t, func() { runMemory([]string{"recall", "deploy tags", "--root", root, "--json"}) })
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(entries) != 1 || entries[0]["text"] != "deploy tags come from a manual release workflow" {
		t.Fatalf("expected the recalled lesson, got %v", entries)
	}
}
