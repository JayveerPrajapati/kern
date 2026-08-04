package memory

import (
	"strings"
	"testing"
	"time"
)

func TestAddAndList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	Add(root, "first lesson")
	Add(root, "second lesson")
	ls := List(root)
	if len(ls) != 2 {
		t.Fatalf("got %d entries, want 2", len(ls))
	}
	// Most recent first.
	if ls[0].Text != "second lesson" || ls[1].Text != "first lesson" {
		t.Fatalf("ordering wrong: %+v", ls)
	}
}

func TestPersistence(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	Add(root, "lesson")
	ls := List(root)
	if len(ls) != 1 || ls[0].Text != "lesson" {
		t.Fatalf("did not persist: %+v", ls)
	}
}

func TestCap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < maxEntries+10; i++ {
		Add(root, strings.Repeat("x", i))
	}
	ls := List(root)
	if len(ls) != maxEntries {
		t.Fatalf("got %d entries, want cap %d", len(ls), maxEntries)
	}
}

func TestEmptyIgnored(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := Add(root, "  "); err != nil {
		t.Fatal(err)
	}
	if ls := List(root); len(ls) != 0 {
		t.Fatalf("empty lesson should be ignored, got %+v", ls)
	}
}

func TestClear(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	Add(root, "lesson")
	if err := Clear(root); err != nil {
		t.Fatal(err)
	}
	if ls := List(root); len(ls) != 0 {
		t.Fatalf("expected cleared, got %+v", ls)
	}
}

func TestTimestampSet(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	Add(root, "lesson")
	e := List(root)[0]
	if e.Time.IsZero() {
		t.Fatal("timestamp not set")
	}
	if e.Time.Location() != time.UTC {
		t.Fatal("timestamps should be UTC")
	}
}
