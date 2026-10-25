package project

import (
	"os"
	"path/filepath"
	"strconv"
)

// IndexingMarkerRel is the project-relative file present while a full index
// build runs. The machine-wide hook guard treats a fresh marker as "kern is
// not ready yet" and lets built-in read/grep through, so an agent is never
// redirected to a tool that can only answer "still building".
const IndexingMarkerRel = ".kern/indexing"

// markIndexing writes the marker and returns the function that removes it.
// It is best-effort: failing to write it only loses the guard's fail-open.
func markIndexing(root string) func() {
	p := filepath.Join(root, filepath.FromSlash(IndexingMarkerRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return func() {}
	}
	if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return func() {}
	}
	return func() { _ = os.Remove(p) }
}
