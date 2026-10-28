package index

// Adjacency persists identity -> sorted caller names: exactly what CallersFor
// returns. One catalog build per save removes that build from every later
// process (resolve, context, explore and the blast-radius walk all call it).
func (ix *Index) buildCallerAdjacency() map[string][]string {
	if ix == nil {
		return nil
	}
	adj := make(map[string][]string, len(ix.Symbols)/4)
	for _, symbol := range ix.Symbols {
		identity := symbol.DeclarationID()
		if _, done := adj[identity]; done {
			continue
		}
		adj[identity] = ix.callersViaCatalog(symbol)
	}
	for identity, callers := range adj {
		if len(callers) == 0 {
			delete(adj, identity)
		}
	}
	return adj
}

// persistAdjacency materializes the map before the snapshot write.
func (ix *Index) persistAdjacency() {
	if ix == nil || ix.Adjacency != nil {
		return
	}
	ix.Adjacency = ix.buildCallerAdjacency()
}
