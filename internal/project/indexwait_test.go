package project

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestIndexWithinTimesOutWhileFirstBuildRuns(t *testing.T) {
	s := New(t.TempDir(), "")
	defer s.Close()
	// Simulate a first build that is in flight and has not produced an index.
	s.mu.Lock()
	s.rebuilding = true
	s.buildStarted = time.Now().Add(-3 * time.Second)
	s.mu.Unlock()

	start := time.Now()
	ix, err := s.IndexWithin(80 * time.Millisecond)
	if ix != nil {
		t.Fatalf("no index exists yet, got %v", ix)
	}
	var be *IndexBuildingError
	if !errors.As(err, &be) {
		t.Fatalf("want *IndexBuildingError, got %v", err)
	}
	if be.Elapsed < 3*time.Second {
		t.Errorf("elapsed should reflect the build start, got %v", be.Elapsed)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("IndexWithin must return near its budget, took %v", time.Since(start))
	}

	// Let the "build" finish: waiters are released and a retry succeeds.
	s.mu.Lock()
	s.rebuilding = false
	s.cond.Broadcast()
	s.mu.Unlock()
}

func TestIndexWithinServesStaleSnapshotOnTimeout(t *testing.T) {
	s := New(t.TempDir(), "")
	defer s.Close()
	stale := &index.Index{}
	s.mu.Lock()
	s.ix = stale
	s.stale = true // forces the slow path
	s.rebuilding = true
	s.mu.Unlock()

	got, err := s.IndexWithin(50 * time.Millisecond)
	if err != nil || got != stale {
		t.Fatalf("want the stale snapshot, got %v, %v", got, err)
	}
}

// TestIndexWithinRealBuildEndToEnd uses a real (not simulated) first build over
// a generated repository: a tiny budget must time out while the build runs,
// the marker must exist during the build and be gone after it, and a retry
// must return the real index built in the background.
func TestIndexWithinRealBuildEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and indexes a few thousand files")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module big\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 40; p++ {
		pkg := filepath.Join(dir, "pkg"+strconv.Itoa(p))
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 50; f++ {
			var b strings.Builder
			b.WriteString("package pkg" + strconv.Itoa(p) + "\n\n")
			for fn := 0; fn < 6; fn++ {
				name := "F" + strconv.Itoa(f) + "x" + strconv.Itoa(fn)
				b.WriteString("func " + name + "() int { return " + strconv.Itoa(fn) + " }\n")
			}
			if err := os.WriteFile(filepath.Join(pkg, "f"+strconv.Itoa(f)+".go"), []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	s := New(dir, "")
	defer s.Close()
	marker := filepath.Join(dir, ".kern", "indexing")

	sawBuilding, sawMarker := false, false
	deadline := time.Now().Add(2 * time.Minute)
	var ix *index.Index
	for time.Now().Before(deadline) {
		got, err := s.IndexWithin(2 * time.Millisecond)
		if err == nil {
			ix = got
			break
		}
		var be *IndexBuildingError
		if !errors.As(err, &be) {
			t.Fatalf("unexpected error: %v", err)
		}
		sawBuilding = true
		if _, serr := os.Stat(marker); serr == nil {
			sawMarker = true
		}
		time.Sleep(time.Millisecond)
	}
	if ix == nil {
		t.Fatal("the background build never produced an index")
	}
	if !sawBuilding {
		t.Skip("build finished inside the 2ms budget on this machine; nothing to assert")
	}
	if !sawMarker {
		t.Error("the indexing marker was never visible while the build ran")
	}
	if len(ix.FileHashes) < 2000 {
		t.Errorf("retry must return the real index, got %d files", len(ix.FileHashes))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("marker must be gone once the build finished")
	}
}

func TestIndexingMarkerLifecycle(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, ".kern", "indexing")
	done := markIndexing(root)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker must exist during the build: %v", err)
	}
	done()
	if _, err := os.Stat(marker); err == nil {
		t.Error("marker must be removed when the build ends")
	}
}

func TestSessionFirstBuildLeavesNoMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir, "")
	defer s.Close()
	if _, err := s.Index(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "indexing")); err == nil {
		t.Error("a finished build must not leave the indexing marker behind")
	}
}

func TestIndexWithinFastPathAndUnlimited(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir, "")
	defer s.Close()
	ix, err := s.IndexWithin(0) // unlimited == Index()
	if err != nil || ix == nil {
		t.Fatalf("unlimited wait: %v, %v", ix, err)
	}
	again, err := s.IndexWithin(time.Minute)
	if err != nil || again != ix {
		t.Fatalf("fresh cached index must be returned as-is: %v, %v", again, err)
	}
}
