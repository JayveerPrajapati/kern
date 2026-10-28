package ignore

import (
	"sync"
	"testing"
)

// TestIgnoredMemoMatchesUncached pins the per-matcher result memo (added
// 2026-10-28 for the freshness-proof stat walk) to the uncached rule
// evaluation for every rule shape: root/nested scopes, dir-only, negation,
// anchored and repeated asks.
func TestIgnoredMemoMatchesUncached(t *testing.T) {
	dir := t.TempDir()
	treeFiles(t, dir, map[string]string{
		".gitignore":     "*.log\nbuild/\n!keep.log\n/cache/\n",
		".kernignore":    "!keep.log\ntmp/\n",
		"sub/.gitignore": "!x.log\n/local/\n",
	})
	m := Load(dir)
	paths := []string{
		"a.log", "keep.log", "build/x.go", "build/keep.go", "build",
		"sub/a.log", "sub/x.log", "sub/keep.log", "sub/local/y.go", "local/y.go",
		"cache/y.go", "sub/cache/y.go", "tmp/z.go", "src/main.go", "sub/main.go", "",
	}
	for round := 0; round < 3; round++ {
		for _, rel := range paths {
			if got, want := m.Ignored(rel), m.match(rel); got != want {
				t.Fatalf("round %d rel=%q: memo=%v uncached=%v", round, rel, got, want)
			}
		}
	}
}

// TestIgnoredMemoConcurrent exercises the memo under concurrent readers — the
// daemon shares one cached matcher across requests.
func TestIgnoredMemoConcurrent(t *testing.T) {
	dir := t.TempDir()
	treeFiles(t, dir, map[string]string{
		".gitignore":     "*.log\nbuild/\n!keep.log\n",
		"sub/.gitignore": "!x.log\n",
	})
	m := LoadCached(dir)
	paths := []string{"a.log", "keep.log", "build/x.go", "sub/a.log", "sub/x.log", "src/main.go"}
	var wg sync.WaitGroup
	mismatch := make(chan string, 1)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for _, rel := range paths {
					if got, want := m.Ignored(rel), m.match(rel); got != want {
						select {
						case mismatch <- rel:
						default:
						}
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(mismatch)
	if rel, ok := <-mismatch; ok {
		t.Fatalf("concurrent memo mismatch for %q", rel)
	}
}
