package index

import (
	"fmt"
	"strings"
)

// Mermaid renders a Mermaid flowchart of a symbol's callers and callees,
// suitable for pasting into any markdown viewer or agent chat.
func (ix *Index) Mermaid(symbol string) string {
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	nodes := map[string]string{symbol: "n1"}
	next := 2
	node := func(s string) string {
		if id, ok := nodes[s]; ok {
			return id
		}
		id := fmt.Sprintf("n%d", next)
		next++
		nodes[s] = id
		return id
	}
	for _, c := range ix.CallersOf(symbol) {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n", node(c), c, node(symbol), symbol)
	}
	for _, c := range dedupeSorted(append([]string{}, ix.Calls[symbol]...)) {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n", node(symbol), symbol, node(c), c)
	}
	return b.String()
}
