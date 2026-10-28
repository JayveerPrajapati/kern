// Package indexmem memoizes a hydrated index for warm, long-lived processes.
//
// The CLI is one process per command, so it pays the full store hydration
// (~0.5 s on a 17k-symbol repo) on every invocation and has nothing to reuse.
// A warm process (the local daemon, `kern daemon serve`) is the opposite: it
// answers dozens of reads against the same index, and re-hydrating per request
// would erase the entire benefit.
//
// Gating lives in the caller: internal/index only consults this package when
// the process opted in (the daemon child). A shared index must never be
// observed by a mutating command, and every one-shot CLI process keeps the
// original semantics by never opting in.
//
// Invalidation is by fingerprint, not by time. The caller passes the store
// files the memo mirrors; their size+mtime identify the on-disk state, so a
// rebuild, an update, or `kern index --force` invalidates the memo on the next
// call without anyone watching the filesystem.
//
// Generic on purpose: this package must not import the index package (that
// would be a cycle), so it stores opaque values keyed by a caller-chosen key.
package indexmem

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	mu    sync.Mutex
	cache = map[string]entry{}
)

type entry struct {
	fp string
	v  any
}

// Load returns the memoized value for key when the store fingerprint is
// unchanged, otherwise calls load and memoizes a successful result. A failed
// load is never memoized, so a corrupt store mid-repair is retried.
func Load[T any](key string, storePaths []string, load func() (T, error)) (T, error) {
	fp := fingerprint(storePaths)
	mu.Lock()
	e, ok := cache[key]
	mu.Unlock()
	if ok && e.fp == fp {
		if v, typeOK := e.v.(T); typeOK {
			return v, nil
		}
	}
	v, err := load()
	if err != nil {
		return v, err
	}
	// Re-read the fingerprint AFTER the load: a rebuild that landed while we
	// were hydrating must not be recorded as the state we memoized.
	mu.Lock()
	cache[key] = entry{fp: fingerprint(storePaths), v: v}
	mu.Unlock()
	return v, nil
}

// fingerprint identifies the on-disk state the memo mirrors. Size+mtime is
// enough: every writer (Save, Update, --force) rewrites these files through a
// temp file + rename, so neither changes silently.
func fingerprint(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(&b, "%s:missing|", p)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d|", p, st.Size(), st.ModTime().UnixNano())
	}
	return b.String()
}

// Reset drops every memoized value (tests; a daemon forcing a re-hydration
// after a repair).
func Reset() {
	mu.Lock()
	cache = map[string]entry{}
	mu.Unlock()
}

// --- generation-scoped cache ---------------------------------------------
//
// Derived answers (a reference scan, a rendered neighborhood) depend on one
// index generation, not on the store's file stats: they are computed FROM a
// loaded index and are valid exactly as long as that *Index value is the one
// in use. Their key is therefore the index pointer itself, and a rebuild — a
// new pointer — drops the whole generation.

var (
	genMu        sync.Mutex
	gen          any
	genNamespace string
	genCache     = map[string]any{}
)

// genCacheMax bounds one generation's entries.
const genCacheMax = 512

// GenGet returns a value memoized for (gen, namespace, key).
func GenGet(genKey any, namespace, key string) (any, bool) {
	genMu.Lock()
	defer genMu.Unlock()
	if gen != genKey || genNamespace != namespace {
		return nil, false
	}
	v, ok := genCache[key]
	return v, ok
}

// GenPut memoizes v for (genKey, namespace, key). A different genKey starts a
// new generation and discards the previous one's entries.
func GenPut(genKey any, namespace, key string, v any) {
	genMu.Lock()
	defer genMu.Unlock()
	if gen != genKey || genNamespace != namespace {
		gen = genKey
		genNamespace = namespace
		genCache = map[string]any{}
	}
	if len(genCache) >= genCacheMax {
		genCache = map[string]any{}
	}
	genCache[key] = v
}

// GenMemo returns the value memoized for (genKey, namespace, key), calling
// compute on a miss. It is the generic form of Get/Put that keeps the
// generation bookkeeping in ONE place (callers do not re-implement it).
func GenMemo[T any](genKey any, namespace, key string, compute func() T) T {
	if v, ok := GenGet(genKey, namespace, key); ok {
		if typed, typeOK := v.(T); typeOK {
			return typed
		}
	}
	v := compute()
	GenPut(genKey, namespace, key, v)
	return v
}

// ResetGenForTest drops the generation cache. Exported for tests in packages
// that use GenGet/GenPut (the cache holds no external resources).
func ResetGenForTest() {
	genMu.Lock()
	defer genMu.Unlock()
	gen = nil
	genNamespace = ""
	genCache = map[string]any{}
}

// Cached returns the memoized value for key when the on-disk generation still
// matches paths (size+mtime of each). It never hydrates; callers Put after
// loading. Use it where the load itself must stay in the caller's package.
func Cached[T any](key string, paths []string) (T, bool) {
	fp := fingerprint(paths)
	mu.Lock()
	e, ok := cache[key]
	mu.Unlock()
	if !ok || e.fp != fp {
		var zero T
		return zero, false
	}
	v, typeOK := e.v.(T)
	return v, typeOK
}

// Put memoizes v for key against the CURRENT generation of paths. The
// fingerprint is re-read here so a rebuild that landed during the load is
// recorded as the newer generation, not the one we hydrated from.
func Put[T any](key string, paths []string, v T) {
	fp := fingerprint(paths)
	mu.Lock()
	cache[key] = entry{fp: fp, v: v}
	mu.Unlock()
}
