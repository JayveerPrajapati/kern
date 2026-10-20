package index

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// bindGoTypes runs the Go compiler-binding pass (go/types + go/ssa-cha) via
// the langparse free function and folds the resulting call-edge updates back
// into the index. The heavy lifting moved to langparse.BindGoTypes.
func (ix *Index) bindGoTypes() error {
	calls, err := langparse.BindGoTypes(ix.Root, ix.Symbols, ix.FileHashes, ix.Calls)
	if err != nil {
		return err
	}
	ix.Calls = calls
	ix.GoTypes = true
	return nil
}
