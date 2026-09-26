package web

import (
	"os"
	"path/filepath"
	"sync"
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

// TestFreshGraphIncrementalSwap verifies the default incremental rebuild path
// in freshGraph (B10): a stale graph rebuilds via index.Update, reusing prior
// per-file results (evidenced by ReusedResults > 0), the swap stays atomic
// (single graphVer bump), and the new index serves subsequent reads.
func TestFreshGraphIncrementalSwap(t *testing.T) {
	app := newEmptyApp(t)
	// Seed the index with two real files so the incremental rebuild has
	// prior results to reuse.
	for _, f := range []struct{ name, body string }{
		{"a.go", "package main\n\nfunc A() int { return 1 }\n"},
		{"b.go", "package main\n\nfunc B() int { return A() }\n"},
	} {
		if err := os.WriteFile(filepath.Join(app.root, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, ix1 := app.freshGraph()
	if ix1 == nil || len(ix1.FileHashes) < 2 {
		t.Fatalf("initial freshGraph: index nil or too small (%d files)", len(ix1.FileHashes))
	}

	// Add one more indexable file: Stale() sees the file-set change.
	if err := os.WriteFile(filepath.Join(app.root, "c.go"), []byte("package main\n\nfunc C() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.staleUntil = time.Time{} // force the cooldown off

	verBefore := app.graphVer
	g2, ix2 := app.freshGraph()
	if g2 == nil || ix2 == nil {
		t.Fatal("incremental freshGraph returned nil graph/index")
	}
	if app.graphVer != verBefore+1 {
		t.Fatalf("graphVer bump: got %d want %d", app.graphVer, verBefore+1)
	}
	if got := ix2.ReusedResults(); got < 2 {
		t.Errorf("KERN_INCREMENTAL=1 rebuild reused %d prior results, want >= 2", got)
	}
	if _, ok := ix2.FileHashes["c.go"]; !ok {
		t.Errorf("new file missing from swapped index")
	}
}

// TestFreshGraphStaleWhileRevalidate: while a rebuild is in flight
// (rebuilding=true), concurrent callers are served the STALE snapshot
// immediately — same graph instance, no graphVer bump, no second rebuild.
// B10.
func TestFreshGraphStaleWhileRevalidate(t *testing.T) {
	app := newEmptyApp(t)
	g1, ix1 := app.freshGraph()

	// Force staleness: add a file and expire the cooldown.
	extra := filepath.Join(app.root, "extra.go")
	if err := os.WriteFile(extra, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}
	app.staleUntil = time.Time{}

	// Simulate an in-flight rebuild claimed by another request.
	app.rebuilding = true
	verBefore := app.graphVer

	g2, ix2 := app.freshGraph()
	if g2 != g1 || ix2 != ix1 {
		t.Fatal("freshGraph during an in-flight rebuild did not serve the stale snapshot")
	}
	if app.graphVer != verBefore {
		t.Fatalf("graphVer bumped during in-flight rebuild: %d -> %d", verBefore, app.graphVer)
	}
	if app.rebuilding != true {
		t.Fatal("in-flight rebuild flag was cleared by a stale-serving caller")
	}
}

// TestFreshGraphSingleFlight: concurrent stale requests trigger exactly ONE
// rebuild (single graphVer bump) — the claim under graphMu is exclusive, so
// burst requests never race to re-index simultaneously. B10.
func TestFreshGraphSingleFlight(t *testing.T) {
	app := newEmptyApp(t)
	app.freshGraph()

	// Force staleness: add a file and expire the cooldown.
	extra := filepath.Join(app.root, "extra.go")
	if err := os.WriteFile(extra, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}
	app.staleUntil = time.Time{}

	verBefore := app.graphVer
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			g, ix := app.freshGraph()
			if g == nil || ix == nil {
				t.Error("freshGraph returned nil graph/index")
			}
		}()
	}
	close(start)
	wg.Wait()

	if app.graphVer != verBefore+1 {
		t.Fatalf("graphVer = %d, want exactly one rebuild (%d + 1)", app.graphVer, verBefore)
	}
}

// TestFreshGraphRebuildsPlatformWithNewGeneration pins the invariant that a
// rebuild swaps EVERY web surface onto the same index generation: index,
// graph (the platform's twin-merged one), arch index, verification engine,
// platform and TaskService. Before this fix the Platform kept serving the
// ORIGINAL graph forever after a swap (the /v1/context + TaskService surfaces
// never saw the fresh index), and a.graph pointed at an un-merged graph while
// the context engine reasoned over the twin-merged one.
func TestFreshGraphRebuildsPlatformWithNewGeneration(t *testing.T) {
	app := newEmptyApp(t)

	// Seed a couple of indexable files so the rebuild has something to
	// change, then force staleness.
	for _, f := range []struct{ name, body string }{
		{"a.go", "package main\n\nfunc A() int { return 1 }\n"},
		{"b.go", "package main\n\nfunc B() int { return A() }\n"},
	} {
		if err := os.WriteFile(filepath.Join(app.root, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(app.root, "c.go"), []byte("package main\n\nfunc C() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.staleUntil = time.Time{}

	// Sanity: the pre-swap surfaces are internally consistent and share the
	// startup generation.
	if app.graph != app.platform.Graph() {
		t.Fatal("startup: a.graph != platform.Graph() (dashboard and context engine disagree)")
	}

	verBefore := app.graphVer
	oldGraph := app.graph
	oldPlatform := app.platform
	g, ix := app.freshGraph()
	if g == nil || ix == nil {
		t.Fatal("freshGraph returned nil graph/index")
	}
	if app.graphVer != verBefore+1 {
		t.Fatalf("graphVer = %d, want %d after rebuild", app.graphVer, verBefore+1)
	}
	if app.platform == oldPlatform {
		t.Fatal("platform was not rebuilt on swap")
	}
	if app.graph == oldGraph {
		t.Fatal("graph was not swapped to the new generation")
	}
	// The old generation stays intact for in-flight readers (pointer swap,
	// never in-place mutation): the pre-swap graph object is untouched.
	if oldGraph == app.platform.Graph() || oldGraph == app.graph {
		t.Fatal("old graph generation was mutated/reused by the swap")
	}
	// EVERY web surface now serves the same generation.
	if app.graph != app.platform.Graph() {
		t.Fatal("post-swap a.graph != platform.Graph() (dashboard and context engine disagree on dimensions)")
	}
	if app.ix != app.platform.Index() {
		t.Fatal("post-swap a.ix != platform.Index()")
	}
	if app.archIndex != app.ix {
		t.Fatal("post-swap archIndex != ix")
	}
	if app.ver != app.platform.VerificationEngine() {
		t.Fatal("post-swap a.ver != platform.VerificationEngine()")
	}
	if app.memories != app.platform.Memory() {
		t.Fatal("post-swap a.memories != platform.Memory()")
	}
	if app.firewall != app.platform.Firewall() {
		t.Fatal("post-swap a.firewall != platform.Firewall()")
	}
	if app.taskSvc == nil || app.taskSvc.Firewall() != app.platform.Firewall() {
		t.Fatal("post-swap taskSvc does not serve the rebuilt platform")
	}
	// The new graph serves the added symbol from the new index generation;
	// the old generation does not (proof the swap is a real generation
	// change, not an in-place refresh).
	if !app.graph.Resolvable("C") {
		t.Fatal("new generation graph does not resolve the added symbol C")
	}
	if oldGraph.Resolvable("C") {
		t.Fatal("old generation graph unexpectedly resolves C (generations mixed?)")
	}
	if id, ok := app.graph.ResolveNodeID("C"); ok {
		if n, ok := app.graph.NodeByID(id); !ok || n.Symbol == nil || n.Symbol.Name != "C" {
			t.Fatalf("NodeByID(%q) = %+v, ok=%v; want the C symbol node", id, n, ok)
		}
	} else {
		t.Fatal("ResolveNodeID(C) failed on the new generation graph")
	}
}
