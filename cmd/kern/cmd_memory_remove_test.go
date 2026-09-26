package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// QA F5: a single bad entry must be deletable without nuking the store.

func TestMemoryRemoveByIndex(t *testing.T) {
	root := newRoot(t)
	for _, l := range []string{"first lesson", "second lesson", "third lesson"} {
		if err := memory.Add(root, l); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := memory.RemoveIndex(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Text != "second lesson" {
		t.Fatalf("removed %q, want %q", removed.Text, "second lesson")
	}
	// List order is newest-first; position 2 of [third, second, first] is
	// "second lesson", leaving [third, first].
	list := memory.List(root)
	if len(list) != 2 || list[0].Text != "third lesson" || list[1].Text != "first lesson" {
		t.Fatalf("remaining entries: %+v", list)
	}
}

func TestMemoryRemoveIndexOutOfRange(t *testing.T) {
	root := newRoot(t)
	if _, err := memory.RemoveIndex(root, 1); err == nil {
		t.Fatal("expected out-of-range error on empty store")
	}
	if err := memory.Add(root, "only"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.RemoveIndex(root, 5); err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestMemoryRemoveByPrefix(t *testing.T) {
	root := newRoot(t)
	for _, l := range []string{"always run go vet", "never commit secrets", "always write tests"} {
		if err := memory.Add(root, l); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := memory.RemovePrefix(root, "never commit")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Text != "never commit secrets" {
		t.Fatalf("removed %q", removed.Text)
	}
	if _, err := memory.RemovePrefix(root, "no such lesson"); err == nil {
		t.Fatal("expected no-match error")
	}
}

func TestMemoryRemoveCLIRendersRemoval(t *testing.T) {
	root := newRoot(t)
	if err := memory.Add(root, "delete me: stale playbook"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runMemory([]string{"remove", "1", "--root", root})
	})
	if !strings.Contains(out, "removed:") || !strings.Contains(out, "delete me: stale playbook") {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(memory.List(root)) != 0 {
		t.Fatal("entry must be gone from the store")
	}
}
