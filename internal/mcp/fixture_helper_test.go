package mcp

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// fixtureRoot returns a fresh testfixture repo root for handler tests that
// need a real, resolvable repository (symbols: NewServer, NewUserService,
// NewDB, ...). The fixture is a tiny git repo, so handler calls that load an
// index or run git commands execute the same code paths as against the full
// kern repo but in milliseconds.
//
// The server's lazy background index preload is disabled (KERN_PRELOAD=0,
// the documented test/CI guard in preloadIndexes) so no goroutine keeps
// writing .kern into the temp repo after the test's handler calls return —
// otherwise t.TempDir cleanup can race the background build.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("KERN_PRELOAD", "0")
	return testfixture.Repo(t)
}
