package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/semcache"
)

// TestCacheSemanticSectionEmpty pins the honest empty state: with no semantic
// cache data, `kern cache` prints the single "no entries yet" line — never a
// confusing zero-table.
func TestCacheSemanticSectionEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.Ensure(); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runCache(nil) })
	if !strings.Contains(out, "semantic cache: no entries yet (populated by optimize/compress/precache runs)") {
		t.Fatalf("expected the no-entries line, got:\n%s", out)
	}
}

// TestCacheSemanticSectionReportsNamespaces pins the semantic section shape:
// namespaces with on-disk indexes are listed (alphabetical) with their entry
// count and the persisted hit rate; a namespace with no recorded lookups
// shows "-" instead of a fabricated 0%.
func TestCacheSemanticSectionReportsNamespaces(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.Ensure(); err != nil {
		t.Fatal(err)
	}
	_ = semcache.Clear("")
	_ = semcache.Store("log", "the database connection failed during migration", "p")
	_ = semcache.Store("prompt", "the queue worker crashed while processing jobs", "p")
	var v string
	// One hit, one miss on "log" (persisted counters): hit rate 50%.
	_, _, _, _ = semcache.Lookup("log", "the database connection failed during the migration run", &v, 0)
	_, _, _, _ = semcache.Lookup("log", "buy a ticket to the opera tonight", &v, 0)

	out := captureStdout(t, func() { runCache(nil) })
	for _, want := range []string{
		"semantic cache: 2 namespaces, 2 entries, ",
		"log",
		"prompt",
		"hit rate 50%",
		"hit rate -",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("cache output missing %q:\n%s", want, out)
		}
	}
}
