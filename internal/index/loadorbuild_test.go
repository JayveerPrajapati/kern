package index

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrBuildRoundtrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadOrBuild(root)
	if err != nil || ix == nil {
		t.Fatalf("first LoadOrBuild: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols from the built index")
	}
	// The build persisted the index (Save); the second call must load it
	// fresh rather than rebuild.
	ix2, err := LoadOrBuild(root)
	if err != nil || ix2 == nil {
		t.Fatalf("second LoadOrBuild: %v", err)
	}
	if len(ix2.Symbols) != len(ix.Symbols) {
		t.Fatalf("symbol count changed between calls: %d vs %d", len(ix2.Symbols), len(ix.Symbols))
	}
}

// captureStderrString runs fn with os.Stderr swapped to a pipe and returns
// everything fn wrote there. A reader goroutine drains the pipe so writers
// never block on the pipe buffer.
func captureStderrString(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() {
		os.Stderr = old
	}()
	fn()
	_ = w.Close()
	return <-done
}

// TestLoadOrBuildAbsentIndexAnnouncesBuild pins the F5 fix: when no
// persisted index exists, LoadOrBuild announces the auto-build on stderr
// BEFORE the multi-second write and reports the finished counts afterwards —
// a read-shaped command (kern search) must never build silently. Tests run
// inside a "<pkg>.test" binary, so the announcement is gated off by default
// (P2-15); the test-only override force-enables it so the real production
// announcement path is still exercised and its wording pinned.
func TestLoadOrBuildAbsentIndexAnnouncesBuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildAnnounceOverride = true
	defer func() { buildAnnounceOverride = false }()
	stderr := captureStderrString(t, func() {
		ix, err := LoadOrBuild(root)
		if err != nil || ix == nil {
			t.Fatalf("LoadOrBuild: %v", err)
		}
	})
	for _, want := range []string{
		"[kern] no index found — building index for " + root,
		"(one-time, background-free)",
		"[kern] indexed ",
		"symbols in ",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestLoadOrBuildSilencesAnnouncementUnderTest pins the P2-15 fix: inside a
// go test binary (named "<pkg>.test") the build announcement must NOT print
// even when no persisted index exists — fixtures across the test fleet that
// build fresh indexes in t.TempDir() stay silent, keeping go test output
// clean.
func TestLoadOrBuildSilencesAnnouncementUnderTest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Guard: the silencing is keyed on the binary name, so this pin only
	// holds while the test binary really is "<pkg>.test" (e.g. not `go run`).
	if base := filepath.Base(os.Args[0]); !strings.HasSuffix(base, ".test") {
		t.Skipf("test binary %q is not named *.test; silencing pin not applicable", base)
	}
	stderr := captureStderrString(t, func() {
		if _, err := LoadOrBuild(root); err != nil {
			t.Fatalf("LoadOrBuild: %v", err)
		}
	})
	for _, banned := range []string{"[kern] no index found", "[kern] indexed"} {
		if strings.Contains(stderr, banned) {
			t.Errorf("announcement %q must be silenced under go test:\n%s", banned, stderr)
		}
	}
}

// TestLoadOrBuildExistingIndexPrintsNoNotice pins the negative: an existing
// FRESH index and an existing STALE index (incremental refresh) must both
// reuse the persisted index without the build notice.
func TestLoadOrBuildExistingIndexPrintsNoNotice(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrBuild(root); err != nil {
		t.Fatalf("initial LoadOrBuild: %v", err)
	}
	stderr := captureStderrString(t, func() {
		if _, err := LoadOrBuild(root); err != nil {
			t.Fatalf("fresh LoadOrBuild: %v", err)
		}
	})
	if strings.Contains(stderr, "[kern] no index found") {
		t.Errorf("fresh index must not print the build notice:\n%s", stderr)
	}
	// Stale the index with a new file: the refresh must go through the
	// incremental Update path, still without the notice.
	if err := os.WriteFile(filepath.Join(root, "more.go"), []byte("package sample\n\nfunc More() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr = captureStderrString(t, func() {
		if _, err := LoadOrBuild(root); err != nil {
			t.Fatalf("stale LoadOrBuild: %v", err)
		}
	})
	if strings.Contains(stderr, "[kern] no index found") {
		t.Errorf("stale refresh must not print the build notice:\n%s", stderr)
	}
}
