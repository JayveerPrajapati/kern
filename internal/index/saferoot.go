package index

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/saferoot"
)

// UnsafeRootError is returned when an automatic first build is refused.
type UnsafeRootError = saferoot.UnsafeRootError

// EnvAllowBroadIndex lets an operator deliberately allow auto-indexing a broad
// directory.
const EnvAllowBroadIndex = saferoot.EnvAllowBroadIndex

// UnsafeAutoIndexRoot returns a non-empty human reason when root must not be
// indexed automatically (see internal/saferoot).
func UnsafeAutoIndexRoot(root string) string { return saferoot.UnsafeAutoIndexRoot(root) }

// HasPersistedIndex reports whether an index store already exists for root,
// i.e. someone deliberately indexed it before. It only stats files: opening
// the SQLite store would itself create <root>/.kern.
func HasPersistedIndex(root string) bool {
	for _, p := range []string{StorePath(root), SQLitePath(root)} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
