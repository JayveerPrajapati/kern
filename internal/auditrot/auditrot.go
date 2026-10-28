// Package auditrot implements the audit-chain retention policy: when the
// active chain crosses a size or entry threshold it is archived (keeping the
// newest N archives) by the governance package, which owns the lock and the
// in-memory chain head.
package auditrot

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Default retention policy (operator decision 2026-10-27): rotate at 10k
// entries or 10 MB, keeping the 5 newest archives. Env overrides exist for
// tests and tighter disk budgets.
const (
	MaxBytesDefault   = 10 << 20
	MaxEntriesDefault = 10000
	KeepDefault       = 5
)

// lastCheck throttles the entry-count read (only needed between the size
// floor and the hard byte ceiling) to once per 30s per process.
var lastCheck atomic.Int64

func envInt(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// Keep returns the number of archives to retain (KERN_AUDIT_ROTATE_KEEP).
func Keep() int {
	return int(envInt("KERN_AUDIT_ROTATE_KEEP", KeepDefault))
}

// CountEntries counts the active chain's non-empty lines. The caller has
// already established the file is large enough to plausibly hold the entry
// cap, so the read is bounded and rare.
func CountEntries(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n
}

// Policy validates the canonical <root>/.kern/audit layout and reports
// whether the active chain should rotate, returning the project root and the
// chain size for the caller's log line.
func Policy(lockDir string) (root string, size int64, ok bool) {
	if filepath.Base(lockDir) != "audit" || filepath.Base(filepath.Dir(lockDir)) != ".kern" {
		return "", 0, false
	}
	size, ok = ShouldRotate(filepath.Join(lockDir, "chain.jsonl"))
	if !ok {
		return "", 0, false
	}
	return filepath.Dir(filepath.Dir(lockDir)), size, true
}

// Prune removes the oldest archive-* directories beyond keep (keep <= 0
// keeps all).
func Prune(auditDir string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(auditDir)
	if err != nil {
		return
	}
	var archives []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "archive-") {
			archives = append(archives, e.Name())
		}
	}
	sort.Strings(archives)
	for len(archives) > keep {
		_ = os.RemoveAll(filepath.Join(auditDir, archives[0]))
		archives = archives[1:]
	}
}

// ShouldRotate reports whether chainPath has crossed the retention policy,
// returning its size for the caller's log line.
func ShouldRotate(chainPath string) (int64, bool) {
	st, err := os.Stat(chainPath)
	if err != nil {
		return 0, false
	}
	size := st.Size()
	maxBytes := envInt("KERN_AUDIT_ROTATE_BYTES", MaxBytesDefault)
	maxEntries := envInt("KERN_AUDIT_ROTATE_ENTRIES", MaxEntriesDefault)
	if maxBytes > 0 && size >= maxBytes {
		return size, true
	}
	if maxEntries > 0 && size >= 1<<20 {
		now := time.Now().UnixNano()
		last := lastCheck.Load()
		if now-last >= int64(30*time.Second) && lastCheck.CompareAndSwap(last, now) {
			return size, int64(CountEntries(chainPath)) >= maxEntries
		}
	}
	return size, false
}
