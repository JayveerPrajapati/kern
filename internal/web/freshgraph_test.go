package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFreshGraphStalenessCooldown verifies the staleness-check cooldown in
// freshGraph: within staleCooldown of a fresh verdict, repeated calls return
// the SAME graph instance without re-walking the tree, even if the project
// changed on disk; after the cooldown expires the change is detected and the
// graph is rebuilt exactly once.
func TestFreshGraphStalenessCooldown(t *testing.T) {
	app := newEmptyApp(t)

	g1, ix1 := app.freshGraph()
	if g1 == nil || ix1 == nil {
		t.Fatal("freshGraph returned nil graph/index")
	}

	// Change the project on disk (add a file → file-set mismatch) while still
	// inside the cooldown window. The cached verdict must be trusted: same
	// instances, no rebuild, graphVer unchanged.
	extra := filepath.Join(app.root, "extra.go")
	if err := os.WriteFile(extra, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}
	verBefore := app.graphVer
	g2, ix2 := app.freshGraph()
	if g2 != g1 || ix2 != ix1 {
		t.Fatal("freshGraph rebuilt within cooldown window — cooldown not effective")
	}
	if app.graphVer != verBefore {
		t.Fatalf("graphVer bumped within cooldown: %d -> %d", verBefore, app.graphVer)
	}

	// Force the cooldown to expire (simulate 1s+ passing) and touch the file
	// so the staleness check sees a real change. The next call must rebuild.
	app.staleUntil = time.Time{}
	if err := os.WriteFile(extra, []byte("package main\n\nfunc x() {}\n"), 0o644); err != nil {
		t.Fatalf("touch extra file: %v", err)
	}
	g3, ix3 := app.freshGraph()
	if g3 == g1 || ix3 == ix1 {
		t.Fatal("freshGraph did not rebuild after cooldown expiry with a changed tree")
	}
	if app.graphVer != verBefore+1 {
		t.Fatalf("graphVer = %d, want %d after rebuild", app.graphVer, verBefore+1)
	}
}
