package intel

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// GraphText renders the one-line call-graph neighbourhood (definition,
// callers, callees) of symbol — the text form `kern graph` prints. The index
// renders "no symbol found: <symbol>" into the output without an error
// return; callers detect that sentinel.
func GraphText(ix *index.Index, symbol string) string {
	return ix.Graph(symbol)
}

// Neighborhood returns the structured graph neighbourhood of symbol. It
// errors when the symbol does not exist in ix.
func Neighborhood(ix *index.Index, symbol string) (index.GraphResult, error) {
	g, ok := ix.Neighborhood(symbol)
	if !ok {
		return index.GraphResult{}, fmt.Errorf("no symbol found: %s", symbol)
	}
	return g, nil
}

// PathBetween returns the shortest call path between two symbols, resolving
// both names through the index. It errors when either symbol is unknown.
func PathBetween(ix *index.Index, from, to string) ([]string, error) {
	f, okFrom := Resolve(ix, from)
	if !okFrom {
		return nil, fmt.Errorf("unknown symbol: %s", from)
	}
	t, okTo := Resolve(ix, to)
	if !okTo {
		return nil, fmt.Errorf("unknown symbol: %s", to)
	}
	return ShortestPath(ix, f, t), nil
}

// WhySymbol explains a symbol's rationale and dependents. It errors when the
// symbol does not exist in ix.
func WhySymbol(ix *index.Index, symbol string) (*WhyInfo, error) {
	info, ok := Why(ix, symbol)
	if !ok {
		return nil, fmt.Errorf("no symbol found: %s", symbol)
	}
	return &info, nil
}
