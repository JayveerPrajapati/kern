package guardrulecache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

func TestCacheRepeatsNonGate(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	cache := newCache(4, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		calls++
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictAllow, Hash: fmt.Sprintf("hash-%s", cmd)}
	})

	first := cache.ClassifyCommandInRoots("pwd", "/repo", nil)
	second := cache.ClassifyCommandInRoots("pwd", "/repo", nil)
	if calls != 1 {
		t.Fatalf("classifier calls = %d, want 1", calls)
	}
	if first.Hash != second.Hash {
		t.Fatalf("second hash = %q, want %q", second.Hash, first.Hash)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache length = %d, want 1", cache.Len())
	}
}

func TestCacheNeverStoresGateOrPathAware(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	cache := newCache(4, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		calls++
		if cmd == "gate" {
			return guardrule.ClassifyResult{Verdict: guardrule.VerdictGate, Hash: cmd}
		}
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictAllow, Hash: cmd, PathAware: true}
	})

	cache.ClassifyCommandInRoots("gate", "/repo", nil)
	cache.ClassifyCommandInRoots("gate", "/repo", nil)
	cache.ClassifyCommandInRoots("path", "/repo", nil)
	cache.ClassifyCommandInRoots("path", "/repo", nil)

	if calls != 4 {
		t.Fatalf("classifier calls = %d, want 4", calls)
	}
	if cache.Len() != 0 {
		t.Fatalf("cache length = %d, want 0", cache.Len())
	}
}

func TestCacheKeysCommandRootAndExtraRoots(t *testing.T) {
	seen := map[string]int{}
	now := time.Unix(0, 0)
	cache := newCache(16, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		key := fmt.Sprintf("%s|%s|%v", cmd, root, roots)
		seen[key]++
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictGoverned, Hash: key}
	})

	cache.ClassifyCommandInRoots("kern verify", "/repo", []string{"/work"})
	cache.ClassifyCommandInRoots("kern verify", "/repo", []string{"/work"})
	cache.ClassifyCommandInRoots("kern verify", "/other", []string{"/work"})
	cache.ClassifyCommandInRoots("kern verify", "/repo", []string{"/other"})
	cache.ClassifyCommandInRoots("kern test", "/repo", []string{"/work"})

	want := map[string]int{
		"kern verify|/repo|[/work]":  1,
		"kern verify|/other|[/work]": 1,
		"kern verify|/repo|[/other]": 1,
		"kern test|/repo|[/work]":    1,
	}
	for key, count := range want {
		if seen[key] != count {
			t.Fatalf("classifier calls for %q = %d, want %d", key, seen[key], count)
		}
	}
}

func TestCacheTTLExpires(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	cache := newCache(4, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		calls++
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictAllow}
	})

	cache.ClassifyCommandInRoots("pwd", "/repo", nil)
	now = now.Add(time.Minute)
	cache.ClassifyCommandInRoots("pwd", "/repo", nil)

	if calls != 2 {
		t.Fatalf("classifier calls = %d, want 2 after TTL expiry", calls)
	}
}

func TestCacheCapacityIsBounded(t *testing.T) {
	now := time.Unix(0, 0)
	cache := newCache(2, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictAllow, Hash: cmd}
	})

	for i := 0; i < 10; i++ {
		cache.ClassifyCommandInRoots(fmt.Sprintf("cmd-%d", i), "/repo", nil)
	}
	if cache.Len() != 2 {
		t.Fatalf("cache length = %d, want 2", cache.Len())
	}
}

func TestCacheConcurrentUse(t *testing.T) {
	now := time.Unix(0, 0)
	cache := newCache(32, time.Minute, func() time.Time { return now }, func(cmd, root string, roots []string) guardrule.ClassifyResult {
		return guardrule.ClassifyResult{Verdict: guardrule.VerdictAllow}
	})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.ClassifyCommandInRoots(fmt.Sprintf("cmd-%d", i%8), "/repo", nil)
		}(i)
	}
	wg.Wait()
	if cache.Len() > 8 {
		t.Fatalf("cache length = %d, want at most 8", cache.Len())
	}
}

// BenchmarkClassifyFreshVsCached measures the cached non-GATE lookup cost
// against the uncached classifier on a representative governed command.
// Acceptance target (plan T-02): cached lookup overhead < 1ms.
func BenchmarkClassifyFreshVsCached(b *testing.B) {
	cmd := "go test ./internal/... 2>&1"
	root := b.TempDir()
	cache := newCache(DefaultCapacity, DefaultTTL, time.Now, guardrule.ClassifyCommandInRoots)
	if got := cache.ClassifyCommandInRoots(cmd, root, nil); got.Verdict != guardrule.VerdictGoverned {
		b.Fatalf("verdict = %v, want governed (benchmark command must stay cacheable)", got.Verdict)
	}
	b.Run("fresh", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			guardrule.ClassifyCommandInRoots(cmd, root, nil)
		}
	})
	b.Run("cached", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache.ClassifyCommandInRoots(cmd, root, nil)
		}
	})
}
