package index

// LoadOrBuild returns the project's symbol index: the persisted
// <root>/.kern/index.json when fresh, or a freshly built one (saved back
// for the next caller) when missing or stale. It is the canonical
// load-or-build shared by the CLI (cmd/kern) and the LSP server —
// previously two byte-identical private copies (blueprint duplication
// debt, G-11). project.Session.Index wraps the same reuse-while-fresh
// contract with session-level caching, a stale cooldown, and SQLite
// preference on top.
//
// Stale indexes are refreshed incrementally via Update whenever a previous
// index loads cleanly (Update re-parses only changed files); any failure
// falls back to a full Build. The explicit `kern index` command bypasses
// this helper entirely (cmd_index.go calls Build directly), so an explicit
// command always means a full rebuild.
func LoadOrBuild(root string) (*Index, error) {
	var prev *Index
	if ix, err := Load(root); err == nil && ix != nil {
		if !ix.Stale() {
			return ix, nil
		}
		prev = ix
	}
	if prev != nil {
		if ix, err := Update(root, prev); err == nil && ix != nil {
			_ = ix.Save()
			return ix, nil
		}
		// Fall through to a full Build on nil/corrupt prev or Update error.
	}
	ix, err := Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}
