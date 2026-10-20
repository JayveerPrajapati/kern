package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/topology"
)

// archFixture writes a two-cluster repo: {main.main, svc.Run, svc.Helper}
// and {main.Local, main.Local2}, with one cross-community edge
// (main.main -> svc.Run) so the architecture report carries a coupling
// bundle. The report is deterministic per index instance.
func archFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("main.go", `package main

import "demo/svc"

func main() { svc.Run() }
func Local() { Local2() }
func Local2() {}
`)
	write("svc/svc.go", `package svc

func Run() { Helper() }
func Helper() {}
`)
	return root
}

// archFixtureExtra adds a third community ({main.Alpha, main.beta}) so the
// rebuilt index instance renders a different architecture report.
func archFixtureExtra(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte(`package main

func Alpha() { beta() }
func beta() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArchUsesSessionCache pins the cached-architecture path: when a session
// is available, Arch must serve the session-cached architecture (computed
// once per index instance) instead of recomputing label propagation + coupling
// bundles per call. The session-cached report is byte-identical to the direct
// computation, and repeated calls stay deterministic.
func TestArchUsesSessionCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := archFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	direct := topology.RenderArch(topology.AnalyzeArchitecture(ix))

	sess := project.New(root, "arch-test")
	defer sess.Close()

	out, err := Arch(context.Background(), ix, sess, map[string]any{})
	if err != nil {
		t.Fatalf("Arch: %v", err)
	}
	if out != direct {
		t.Errorf("Arch via session cache differs from direct computation:\n--- cached ---\n%s\n--- direct ---\n%s", out, direct)
	}
	again, err := Arch(context.Background(), ix, sess, map[string]any{})
	if err != nil {
		t.Fatalf("Arch 2: %v", err)
	}
	if again != out {
		t.Error("Arch output changed between calls on the same index instance")
	}
}

// TestArchNilSessionFallsBackToDirect pins the fallback: without a session
// the direct AnalyzeArchitecture call is kept, so the report is unchanged.
func TestArchNilSessionFallsBackToDirect(t *testing.T) {
	root := archFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	direct := topology.RenderArch(topology.AnalyzeArchitecture(ix))

	out, err := Arch(context.Background(), ix, nil, map[string]any{})
	if err != nil {
		t.Fatalf("Arch: %v", err)
	}
	if out != direct {
		t.Errorf("nil-session Arch differs from direct computation:\n%s", out)
	}
}

// TestArchCacheHitDoesNotRecompute proves a cache hit skips the expensive
// computation: the session cache is keyed to the index instance it was warmed
// on, so with a warm cache Arch must serve the cached report even when handed
// a different index — recomputing would surface the new index's structure.
// The session is created AFTER the tree mutation so the file watcher cannot
// invalidate the warm cache mid-test (fswatch emits only real post-start
// events, never a baseline replay).
func TestArchCacheHitDoesNotRecompute(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := archFixture(t)
	ix1, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	direct1 := topology.RenderArch(topology.AnalyzeArchitecture(ix1))

	// Second index instance with a different community structure.
	archFixtureExtra(t, root)
	ix2, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	direct2 := topology.RenderArch(topology.AnalyzeArchitecture(ix2))
	if direct2 == direct1 {
		t.Fatal("precondition failed: fixture v2 must differ from v1")
	}

	sess := project.New(root, "arch-test")
	defer sess.Close()
	// Warm the session cache on ix1 — the instance the cache is keyed to.
	if got := topology.RenderArch(sess.Architecture(ix1)); got != direct1 {
		t.Fatal("precondition failed: warm-up must match the direct computation")
	}

	out, err := Arch(context.Background(), ix2, sess, map[string]any{})
	if err != nil {
		t.Fatalf("Arch: %v", err)
	}
	if out != direct1 {
		t.Errorf("cache hit re-computed: got the ix2-based report, want the cached ix1-based report")
	}
	if out == direct2 {
		t.Error("cache hit must not reflect the newly passed index (recompute happened)")
	}
}

// TestArchRecomputesAfterRebuild pins the invalidation contract: Session.Index()
// clears the derived computation caches when a rebuild publishes a fresh index
// instance, so the next Arch call reflects the new tree instead of serving a
// stale report.
func TestArchRecomputesAfterRebuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := archFixture(t)
	sess := project.New(root, "arch-test")
	defer sess.Close()

	ix1, err := sess.Index()
	if err != nil {
		t.Fatalf("Index 1: %v", err)
	}
	out1, err := Arch(context.Background(), ix1, sess, map[string]any{})
	if err != nil {
		t.Fatalf("Arch 1: %v", err)
	}
	if out1 != topology.RenderArch(topology.AnalyzeArchitecture(ix1)) {
		t.Fatal("Arch after first build differs from direct computation")
	}

	// Rebuild with a new community and a fresh index instance.
	archFixtureExtra(t, root)
	sess.Invalidate()
	ix2, err := sess.Index()
	if err != nil {
		t.Fatalf("Index 2: %v", err)
	}
	if ix2 == ix1 {
		t.Fatal("precondition failed: rebuild must produce a new index instance")
	}
	out2, err := Arch(context.Background(), ix2, sess, map[string]any{})
	if err != nil {
		t.Fatalf("Arch 2: %v", err)
	}
	if out2 != topology.RenderArch(topology.AnalyzeArchitecture(ix2)) {
		t.Error("Arch after rebuild differs from the direct computation over the fresh index")
	}
	if out2 == out1 {
		t.Error("Arch after rebuild still served the pre-rebuild result (stale cache)")
	}
}
