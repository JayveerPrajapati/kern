package app

// platform_memo.go memoizes the index-only Platform for warm processes.
//
// `kern impact`'s default path builds an index-only Platform per invocation:
// index.LoadOrBuild plus intel.FromIndex (the call graph). In a one-shot CLI
// process both are unavoidable; in the daemon (which answers many reads from
// one process) rebuilding the graph per request is pure waste — measured
// ~0.3-0.5 s of the ~0.66 s `kern impact` warm cost.
//
// The memo is keyed by the INDEX POINTER, not by time or by path: when a warm
// process serves a memoized index (internal/indexmem), the same *Index comes
// back for the same store state, and a rebuild produces a NEW pointer — so the
// graph can never outlive the index it was built from. One-shot processes
// leave MemoizePlatform off and keep the original construction path.

import (
	"sync"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// MemoizePlatform makes NewIndexOnly reuse the Platform built for the same
// *index.Index. Set it ONLY in a warm, read-only process (the daemon child).
var MemoizePlatform bool

var (
	platformMu    sync.Mutex
	platformCache = map[*index.Index]*Platform{}
)

// memoizedIndexOnly returns the cached Platform for ix, building it on a miss.
func memoizedIndexOnly(root string, ix *index.Index) *Platform {
	platformMu.Lock()
	if p, ok := platformCache[ix]; ok {
		platformMu.Unlock()
		return p
	}
	platformMu.Unlock()

	g := intel.FromIndex(ix)
	p := &Platform{root: root, ix: ix, graph: &g}

	platformMu.Lock()
	// Keep exactly one entry: a rebuild swaps the index pointer and the old
	// platform (with its graph) becomes unreachable, which is the intent.
	platformCache = map[*index.Index]*Platform{ix: p}
	platformMu.Unlock()
	return p
}
