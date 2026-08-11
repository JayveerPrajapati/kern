package precache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWarmMissingRoot(t *testing.T) {
	rep := Warm(filepath.Join(t.TempDir(), "nope"))
	if !rep.SourceMiss {
		t.Fatal("expected source miss")
	}
}

func TestWarmSummarizes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Fn() {}\n"), 0o644)
	rep := Warm(root)
	if rep.Warmed != 1 {
		t.Fatalf("expected 1 warmed summary, got %+v", rep)
	}
	// Second pass should be cache hits.
	rep2 := Warm(root)
	if rep2.CacheHits < 1 {
		t.Fatalf("expected cache hit on second pass, got %+v", rep2)
	}
	if rep2.Warmed != 0 {
		t.Fatalf("expected no new warms, got %+v", rep2)
	}
}

func TestWarmSkipsIgnoredDirs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "node_modules"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "node_modules", "big.go"), []byte("package big\n"), 0o644)
	rep := Warm(root)
	if rep.Warmed != 1 {
		t.Fatalf("node_modules must be skipped, got %+v", rep)
	}
}

func TestWarmDocsWhenMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("kern prewarms document vectors ahead of interactive queries so searches are instant when the user runs them\n"), 0o644)
	rep := Warm(root)
	if !rep.DocsSaved || rep.DocChunks == 0 {
		t.Fatalf("expected doc index save, got %+v", rep)
	}
}

func TestWarmSkipsAgentConfigDirs(t *testing.T) {
	// ix-12: precache must skip the same agent/tooling dirs as the index.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	for _, dir := range []string{".venv", "__pycache__", ".next", "target", ".opencode"} {
		_ = os.MkdirAll(filepath.Join(root, dir), 0o755)
		_ = os.WriteFile(filepath.Join(root, dir, "junk.go"), []byte("package junk\n"), 0o644)
	}
	rep := Warm(root)
	if rep.Warmed != 1 {
		t.Fatalf("expected only a.go warmed, got %+v", rep)
	}
}

func TestWatchStops(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	stop := make(chan struct{})
	ch := Watch(root, 20*time.Millisecond, stop)
	reports := 0
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				break loop
			}
			if r.Warmed > 0 {
				reports++
				close(stop)
			}
		case <-timeout:
			break loop
		}
	}
	if reports == 0 {
		t.Fatal("watch never emitted a warm pass")
	}
}
