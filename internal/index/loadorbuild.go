package index

// LoadOrBuild returns the project's symbol index: the persisted
// <root>/.kern/index.json when fresh, or a freshly built one (saved back
// for the next caller) when missing or stale. It is the canonical
// load-or-build shared by the CLI (cmd/kern) and the LSP server —
// previously two byte-identical private copies (blueprint duplication
// debt, G-11). project.Session.Index wraps the same reuse-while-fresh
// contract with session-level caching, a stale cooldown, and SQLite
// preference on top.
func LoadOrBuild(root string) (*Index, error) {
	if ix, err := Load(root); err == nil && ix != nil && !ix.Stale() {
		return ix, nil
	}
	ix, err := Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}
