package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestMemoryRememberAutoFlag pins `kern remember --auto`: the lesson is
// written as an automatic capture (v1 Source "auto") regardless of content —
// the flag the plugin passes in Wave 2 for session captures.
func TestMemoryRememberAutoFlag(t *testing.T) {
	root := newRoot(t)
	captureStdout(t, func() {
		runRemember([]string{"--auto", "Command failed: ls: nope: no such file or directory", "--root", root})
	})
	entries := memory.List(root)
	if len(entries) != 1 || entries[0].Source != "auto" {
		t.Fatalf("--auto remember must store one Source auto entry, got %+v", entries)
	}
	// The flag must tag any text auto, even non-auto-shaped lessons.
	captureStdout(t, func() {
		runRemember([]string{"--auto", "a deliberate-but-flagged lesson", "--root", root})
	})
	entries = memory.List(root)
	if len(entries) != 2 || entries[0].Source != "auto" {
		t.Fatalf("--auto must tag any lesson auto, got %+v", entries)
	}
}

// TestMemoryRememberAutoShapedStaysGenuine pins L5-4 (supersedes the F2
// prefix-routing pin): `kern remember` WITHOUT --auto stores a GENUINE
// lesson (Source "user" in both stores) no matter what the text starts
// with — a real lesson like "Edited files must always ship with tests"
// must stay recallable and prune --auto-safe. The plugin passes --auto for
// its session captures (pinned by TestMemoryRememberAutoFlag); text-shape
// guessing never routes an explicit write.
func TestMemoryRememberAutoShapedStaysGenuine(t *testing.T) {
	root := newRoot(t)
	captureStdout(t, func() {
		runRemember([]string{"Edited internal/api/handlers.go", "--root", root})
	})
	entries := memory.List(root)
	if len(entries) != 1 || entries[0].Source != "user" {
		t.Fatalf("plain auto-shaped remember must store one Source user entry, got %+v", entries)
	}
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 || mems[0].Source != "user" {
		t.Fatalf("typed store must hold the lesson as Source user, got %+v", mems)
	}
}

// TestMemoryPruneAutoCLI pins `kern memory prune --auto` end-to-end: Source
// "auto" captures are removed from both stores, and genuine user/learning
// lessons survive and stay recallable — including explicit lessons with
// auto-shaped prefixes (L5-4: "Edited ..." text written via the explicit
// path is genuine; duplicates collapse to one copy).
func TestMemoryPruneAutoCLI(t *testing.T) {
	root := newRoot(t)
	if _, err := memory.AddExplicit(root, "Edited /tmp/lane/a.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AddExplicit(root, "Edited /tmp/lane/a.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AddExplicit(root, "the payment service retries idempotency keys"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AddAuto(root, "Command failed: ls: nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.NewMemoryStore(root).Add(domain.Memory{
		Type:    domain.MemoryConstraint,
		Content: "redis keys must contain tenant id",
		Source:  "learning",
		Scope:   "project",
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		runMemory([]string{"prune", "--auto", "--root", root})
	})
	if !strings.Contains(out, "pruned:") {
		t.Fatalf("expected prune summary, got %q", out)
	}

	// Typed store: the auto capture is gone; the duplicated Edited lesson
	// collapses to one genuine copy; payment + redis survive.
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 3 {
		t.Fatalf("typed store after prune = %d entries, want 3: %+v", len(mems), mems)
	}
	edited := 0
	for _, m := range mems {
		if m.Content == "Edited /tmp/lane/a.go" {
			edited++
		}
		if m.Content == "Command failed: ls: nope" {
			t.Fatalf("auto capture survived prune: %+v", m)
		}
	}
	if edited != 1 {
		t.Fatalf("explicit auto-shaped lesson must survive prune as ONE collapsed copy, got %d: %+v", edited, mems)
	}

	// v1 store keeps the explicit lessons (Edited collapsed to one + payment),
	// still recallable.
	if ls := memory.List(root); len(ls) != 2 {
		t.Fatalf("v1 store after prune = %d entries, want 2: %+v", len(ls), ls)
	}
	if got := memory.Recall(root, "payment retries idempotency", 5); len(got) != 1 {
		t.Fatalf("genuine lesson lost after prune: %+v", got)
	}
}

// TestMemoryPruneRequiresAutoFlag: prune without --auto is a usage error —
// prune only ever removes automatic captures, and the flag is the explicit
// confirmation.
func TestMemoryPruneRequiresAutoFlag(t *testing.T) {
	root := newRoot(t)
	expectExit(t, 2, func() { runMemory([]string{"prune", "--root", root}) })
}

// TestMemoryRemoveMirrorsTypedStore pins F2: `kern memory remove` must also
// delete the lesson from the typed store (buddy's "Project memory") — it
// previously touched only the v1 store.
func TestMemoryRemoveMirrorsTypedStore(t *testing.T) {
	root := newRoot(t)
	if _, err := memory.AddExplicit(root, "delete me: stale playbook"); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { runMemory([]string{"remove", "1", "--root", root}) })
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mems {
		if m.Content == "delete me: stale playbook" {
			t.Fatalf("removed lesson still in typed store: %+v", m)
		}
	}
	if ls := memory.List(root); len(ls) != 0 {
		t.Fatalf("v1 store not removed: %+v", ls)
	}
}

// TestMemoryClearClearsBothStores pins F2: `kern memory --clear` empties the
// typed store too — it previously emptied only v1.
func TestMemoryClearClearsBothStores(t *testing.T) {
	root := newRoot(t)
	if _, err := memory.AddExplicit(root, "a lesson to clear"); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { runMemory([]string{"--clear", "--root", root}) })
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 0 {
		t.Fatalf("typed store not cleared: %+v", mems)
	}
	if ls := memory.List(root); len(ls) != 0 {
		t.Fatalf("v1 store not cleared: %+v", ls)
	}
}
