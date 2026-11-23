package index

import (
	"time"
)

func fixtureIndex(dir string) *Index {
	ix := &Index{
		Root:          dir,
		Version:       indexVersion,
		UpdatedAt:     time.Now(),
		Symbols:       []Symbol{{Name: "Alpha", Kind: "func", File: "a.go", Line: 1}},
		FileHashes:    map[string]string{"a.go": "h"},
		Calls:         map[string][]CallEdge{},
		Callers:       map[string][]string{},
		Pkgs:          map[string]*Pkg{},
		BuildStats:    BuildStats{Kind: "test", Ms: 1},
		SymbolsByFile: map[string][]Symbol{},
	}
	ix.initMaps()
	return ix
}
