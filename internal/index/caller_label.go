package index

// ambiguousDef reports whether name has definitions in more than one file, in
// which case a name-only lookup cannot say which one a caller refers to.
func ambiguousDef(ix *Index, name string) bool {
	defs := ix.symbolsFor(name)
	for i := 1; i < len(defs); i++ {
		if defs[i].File != defs[0].File {
			return true
		}
	}
	return false
}
