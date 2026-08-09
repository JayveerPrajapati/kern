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
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			return ""
		}
	}
	root := defs[0]
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	nodes := map[string]string{root.FullName(): "n1"}
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
	for _, c := range ix.CallersFor(root) {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n", node(c), c, node(root.FullName()), root.FullName())
	}
	for _, c := range ix.CallsFor(root) {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n", node(root.FullName()), root.FullName(), node(c), c)
	}
	return b.String()
}
