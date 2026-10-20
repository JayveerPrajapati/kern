package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// The text-mode renderers for memory list/recall are the shared
// memory.FormatEntries output; these tests pin the surface contract that the
// shared renderer must keep (auto labels on list, no-match hint on recall —
// `kern memory recall` previously printed nothing on no-match before the
// surfaces were consolidated).

func TestMemoryListTextAutoLabel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := memory.AddAuto(root, "User: how do I ship this?"); err != nil {
		t.Fatalf("memory.AddAuto: %v", err)
	}
	if err := memory.Add(root, "deploys run via the release workflow"); err != nil {
		t.Fatalf("memory.Add: %v", err)
	}
	out := captureStdout(t, func() { runMemory([]string{"list", "--root", root}) })
	if !strings.Contains(out, "[auto] User: how do I ship this?") {
		t.Fatalf("expected [auto] label for auto-captured entry, got %q", out)
	}
	if !strings.Contains(out, "deploys run via the release workflow") {
		t.Fatalf("expected deliberate lesson without label, got %q", out)
	}
}

// TestMemoryBarePrintsHelp pins F4: bare `kern memory` prints the memory
// help instead of silently falling through to an (often empty) list print.
func TestMemoryBarePrintsHelp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out := captureStdout(t, func() { runMemory([]string{"--root", t.TempDir()}) })
	if !strings.Contains(out, "kern memory") || !strings.Contains(out, "add <lesson>") {
		t.Fatalf("expected the memory help, got %q", out)
	}
}

func TestMemoryRecallTextNoMatchHint(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := memory.Add(root, "deploy tags come from a manual release workflow"); err != nil {
		t.Fatalf("memory.Add: %v", err)
	}
	out := captureStdout(t, func() { runMemory([]string{"recall", "xyzzy plugh unrelated", "--root", root}) })
	if !strings.Contains(out, memory.NoRecallMatch) {
		t.Fatalf("expected no-match hint on `kern memory recall`, got %q", out)
	}
	hit := captureStdout(t, func() { runMemory([]string{"recall", "deploy tags", "--root", root}) })
	if !strings.Contains(hit, "deploy tags") {
		t.Fatalf("expected recalled lesson, got %q", hit)
	}
	if strings.Contains(hit, memory.NoRecallMatch) {
		t.Fatalf("no-match hint leaked into a hit result, got %q", hit)
	}
}

// TestMemoryListEmptyFriendlyMessage pins QA 6.3: `kern memory list` on an
// empty store prints a friendly "no lessons stored yet" pointer (exit stays
// 0), while --json keeps the machine-readable empty array.
func TestMemoryListEmptyFriendlyMessage(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	out := captureStdout(t, func() { runMemory([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no lessons stored yet") {
		t.Fatalf("expected a friendly empty-store message, got %q", out)
	}
	if strings.Contains(out, "usage:") {
		t.Fatalf("empty-store list must not print usage (it is not an error), got %q", out)
	}
	// --json stays machine-readable: an empty array, not prose.
	outJSON := captureStdout(t, func() { runMemory([]string{"list", "--json", "--root", root}) })
	if !strings.Contains(outJSON, "[]") {
		t.Fatalf("--json on an empty store must print an empty array, got %q", outJSON)
	}
	if strings.Contains(outJSON, "no lessons stored yet") {
		t.Fatalf("--json must not carry the prose hint, got %q", outJSON)
	}
}
