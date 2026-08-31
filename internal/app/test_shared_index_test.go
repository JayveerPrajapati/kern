package app

import (
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// sharedTestIndex is built exactly once per test run: every test that needs a
// Platform over the real repo root reuses this prebuilt index instead of
// re-running index.Build over the whole kern repo (~3s each, ~70-80s wasted
// across the suite). Platform treats the index as read-only after
// construction, so sharing one instance across tests is safe.
var (
	sharedTestIndexOnce sync.Once
	sharedTestIndex     *index.Index
	sharedTestIndexErr  error
)

// sharedTestRepoIndex returns the prebuilt index over the kern repo root,
// building it once via sync.Once. The index is read-only after Build, so every
// test in the package may share it. Build errors are fatal: no test can make
// progress without the index.
func sharedTestRepoIndex(t *testing.T) *index.Index {
	t.Helper()
	sharedTestIndexOnce.Do(func() {
		sharedTestIndex, sharedTestIndexErr = index.Build("../..")
	})
	if sharedTestIndexErr != nil {
		t.Fatalf("app: shared repo index: %v", sharedTestIndexErr)
	}
	return sharedTestIndex
}
