package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/semcache"
)

// runCache reports the cache dir health and runs the maintain pass
// (docs/audit/next-plan-gaps.md): entry count + total size of all *.json and
// *.json.gz cache files, then MaintainDefaults on the cache root. --dry-run
// reports what the pass WOULD archive/evict without touching anything.
func runCache(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("cache: unexpected argument %q", args[0])
	}
	dir := cache.Dir()
	entries, size := cacheStats(dir)
	fmt.Printf("cache dir: %s\n", dir)
	fmt.Printf("entries: %d, size: %s\n", entries, humanBytes(size))
	budget := cache.MaxBudgetBytes()
	if budget > 0 {
		pct := float64(size) / float64(budget) * 100
		fmt.Printf("ceiling: %s (cache.max_mb; 0 disables), usage: %.1f%%\n", humanBytes(budget), pct)
	} else {
		fmt.Printf("ceiling: disabled (cache.max_mb <= 0)\n")
	}
	archived, evicted, trimmed, err := cache.MaintainDefaults(dir, f.dryRun)
	if err != nil {
		fatal("cache: %v", err)
	}
	if f.dryRun {
		fmt.Printf("dry-run — would archive: %d, would evict: %d, would trim: %d\n", archived, evicted, trimmed)
	} else {
		fmt.Printf("archived: %d, evicted: %d, trimmed: %d\n", archived, evicted, trimmed)
	}
	printSemanticCache()
}

// printSemanticCache renders the semantic-cache section of `kern cache`: a
// summary line plus one row per namespace that has an on-disk index (entries
// and the hit rate from the PERSISTED counters, which compound across process
// restarts — so the number shows whether the cache is actually compounding).
// A namespace with no recorded lookups shows "-" for its hit rate; when the
// semantic cache has never been populated, a single "no entries yet" line is
// printed instead of an empty table.
func printSemanticCache() {
	namespaces, err := semcache.Namespaces()
	if err != nil {
		fatal("cache: %v", err)
	}
	if len(namespaces) == 0 {
		fmt.Println("semantic cache: no entries yet (populated by optimize/compress/precache runs)")
		return
	}
	totalEntries := 0
	var totalBytes int64
	for _, s := range namespaces {
		totalEntries += s.Entries
		totalBytes += s.Bytes
	}
	fmt.Printf("semantic cache: %d namespaces, %d entries, %s\n", len(namespaces), totalEntries, humanBytes(totalBytes))
	for _, s := range namespaces {
		rate := "-"
		if s.Hits+s.Misses > 0 {
			rate = fmt.Sprintf("%d%%", int(s.HitRate()*100))
		}
		fmt.Printf("  %-10s %6d entries   hit rate %s\n", s.Namespace, s.Entries, rate)
	}
}

// cacheStats counts every *.json / *.json.gz file under dir and their total
// size. It walks the whole tree (read-only) so the reported footprint is the
// real one even though Maintain itself only touches a directory's top level.
func cacheStats(dir string) (entries int, size int64) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".json.gz") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			entries++
			size += info.Size()
		}
		return nil
	})
	return entries, size
}

// humanBytes renders a byte count as a compact human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
