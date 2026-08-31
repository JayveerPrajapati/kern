package cache

import "sync"

// pathLocks is a package-level registry of mutexes keyed by filesystem path.
// It exists because JSON-file stores (TaskStore, ArtifactStore, SnapshotStore)
// are instantiated multiple times per project root within one process — the
// MCP server, the web app, and the CLI each build their own store instance
// pointing at the same backing file. A per-instance sync.Mutex does NOT
// serialize those writers: two instances can read-modify-write the same file
// concurrently and lose each other's updates. Keying by path makes every
// instance that touches the same file serialize on one mutex.
// The map is never shrunk; each entry is a tiny mutex and the number of
// distinct store paths is bounded by the number of project roots used in a
// process lifetime. This is the same registry pattern used for the SQLite
// FTS store's per-database locks.
var pathLocks sync.Map // map[string]*sync.Mutex

// PathLock returns the process-wide mutex guarding the given filesystem path.
// All instances of every store that read-modify-write this path must hold it
// around their load->modify->save critical section.
func PathLock(path string) *sync.Mutex {
	mu, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}
