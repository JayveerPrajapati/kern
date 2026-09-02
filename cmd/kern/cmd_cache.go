package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// runCache reports the cache dir health and runs the G-7 maintain pass
// (docs/audit/next-plan-gaps.md): entry count + total size of all *.json and
// *.json.gz cache files, then MaintainDefaults on the cache root. --dry-run
// reports what the pass WOULD archive/evict without touching anything.
func runCache(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) > 0 {
		fatalUsage("cache: unexpected argument %q", args[0])
	}
	dir := cache.Dir()
	entries, size := cacheStats(dir)
	fmt.Printf("cache dir: %s\n", dir)
	fmt.Printf("entries: %d, size: %s\n", entries, humanBytes(size))
	archived, evicted, err := cache.MaintainDefaults(dir, f.dryRun)
	if err != nil {
		fatal("cache: %v", err)
	}
	if f.dryRun {
		fmt.Printf("dry-run — would archive: %d, would evict: %d\n", archived, evicted)
		return
	}
	fmt.Printf("archived: %d, evicted: %d\n", archived, evicted)
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
