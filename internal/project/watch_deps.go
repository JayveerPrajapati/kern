package project

import (
	"path/filepath"
	"strings"
	"time"
)

// relatedFile reports whether two watched files are "related" for debounce
// purposes: same directory (files co-edited in one package), or the same
// stem (base name minus extension and a trailing _test/_spec suffix) with a
// different base name (foo.go and foo_test.go). Editing related files
// back-to-back should rebuild once, not twice. Both paths are relative to
// the watch root.
//
// Two files are NOT related when they merely share a base name in different
// directories (a/x.go vs b/x.go are separate packages), or when two
// differently-named files sit at the root (x.go vs y.go are separate
// modules).
func relatedFile(a, b string) bool {
	if d := filepath.Dir(a); d == filepath.Dir(b) && d != "." {
		return true
	}
	return stem(a) == stem(b) && filepath.Base(a) != filepath.Base(b)
}

// stem returns the base name of p minus its extension and minus a trailing
// _test/_spec suffix, e.g. foo_test.go -> foo, foo_spec.rb -> foo.
func stem(p string) string {
	base := filepath.Base(p)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSuffix(base, "_test")
	base = strings.TrimSuffix(base, "_spec")
	return base
}

// shouldExtendDependency reports whether the debounce window should be
// extended: there exists a related pair (a, b) in recent, a != b, where at
// least one of them was touched within the last window (a still-active edit
// burst across related files). The pair loop is fine because recent is
// bounded (≤128 entries).
func shouldExtendDependency(recent map[string]time.Time, now time.Time, window time.Duration) bool {
	for a, ta := range recent {
		for b, tb := range recent {
			if a == b || !relatedFile(a, b) {
				continue
			}
			if now.Sub(ta) <= window || now.Sub(tb) <= window {
				return true
			}
		}
	}
	return false
}
