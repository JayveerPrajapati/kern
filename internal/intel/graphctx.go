package intel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/index"
)

const (
	// GraphCtxDefaultTokens is the default token budget for a names-only
	// graph context. 400 tokens fits a few dozen adjacency rows — the
	// minimal caller-first answer, not the source text.
	GraphCtxDefaultTokens = 400
	// maxCommunityMembers caps the community member list shown beside the
	// root symbol so a huge cluster cannot blow the budget.
	maxCommunityMembers = 20
)

// GraphCtx returns a token-budgeted, names-only graph context for a symbol:
// its definition line, callers first, then callees, every adjacency edge
// tagged with its confidence (EXTRACTED/INFERRED/AMBIGUOUS), and the community
// the symbol belongs to. Calls to interface methods carry dispatch hints
// listing the concrete implementations the call could reach. maxTokens <= 0
// means no budget cap.
func GraphCtx(ix *index.Index, symbol string, maxTokens int) (string, error) {
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	resolved, ok := Resolve(ix, symbol)
	if !ok {
		// An interface method ("Store.Fetch") has no symbol of its own — the
		// index records the receiver-qualified call target, not a definition.
		// Answer with the dispatch it can take instead of failing.
		if recv, meth, isIface := graphInterfaceMethod(ix, symbol); isIface {
			return graphInterfaceMethodCtx(ix, symbol, recv, meth, maxTokens)
		}
		return "", fmt.Errorf("unknown symbol: %s", symbol)
	}
	g, ok := ix.Neighborhood(resolved)
	if !ok {
		return "", fmt.Errorf("unknown symbol: %s", symbol)
	}

	var b strings.Builder
	if def := graphDefNode(g, resolved); def != nil {
		b.WriteString(fmt.Sprintf("graph %s (%s) — %s:%d\n", def.Name, def.Kind, def.File, def.Line))
	} else {
		b.WriteString("graph " + resolved + "\n")
	}
	if impls := graphDispatchImpls(ix, resolved); len(impls) > 0 {
		b.WriteString("  dispatch (INFERRED): " + strings.Join(impls, ", ") + "\n")
	}

	callers := graphNeighbors(ix, g, resolved, "caller")
	if len(callers) > 0 {
		b.WriteString(fmt.Sprintf("callers (%d):\n", len(callers)))
		for _, row := range callers {
			b.WriteString("  " + row.String() + "\n")
		}
	}

	callees := graphNeighbors(ix, g, resolved, "callee")
	if len(callees) > 0 {
		b.WriteString(fmt.Sprintf("callees (%d):\n", len(callees)))
		for _, row := range callees {
			b.WriteString("  " + row.String() + "\n")
			if impls := graphDispatchImpls(ix, row.name); len(impls) > 0 {
				b.WriteString("    dispatch (INFERRED): " + strings.Join(impls, ", ") + "\n")
			}
		}
	}

	if members := graphCommunityMembers(ix, resolved); len(members) > 0 {
		shown := members
		if len(shown) > maxCommunityMembers {
			shown = append(shown[:maxCommunityMembers], "…")
		}
		b.WriteString(fmt.Sprintf("community (%d members): %s\n", len(members), strings.Join(shown, ", ")))
	}

	text := strings.TrimSpace(b.String())
	if maxTokens > 0 {
		text = budget.Fit(text, maxTokens)
	}
	return text, nil
}

// graphInterfaceMethodCtx renders the context for an interface method root:
// its dispatch targets (the concrete implementations the call sites can reach)
// and its callers (the call sites themselves). There is no definition line —
// interface methods are not symbols.
func graphInterfaceMethodCtx(ix *index.Index, symbol, recv, meth string, maxTokens int) (string, error) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("graph %s (interface method)\n", symbol))
	if impls := graphDispatchImpls(ix, symbol); len(impls) > 0 {
		b.WriteString("  dispatch (INFERRED): " + strings.Join(impls, ", ") + "\n")
	}
	if callers := graphInterfaceCallers(ix, symbol); len(callers) > 0 {
		b.WriteString(fmt.Sprintf("callers (%d):\n", len(callers)))
		for _, row := range callers {
			b.WriteString("  " + row.String() + "\n")
		}
	}
	if members := graphCommunityMembers(ix, symbol); len(members) > 0 {
		shown := members
		if len(shown) > maxCommunityMembers {
			shown = append(shown[:maxCommunityMembers], "…")
		}
		b.WriteString(fmt.Sprintf("community (%d members): %s\n", len(members), strings.Join(shown, ", ")))
	}

	text := strings.TrimSpace(b.String())
	if maxTokens > 0 {
		text = budget.Fit(text, maxTokens)
	}
	return text, nil
}

// graphInterfaceCallers returns the call sites of an interface method as
// adjacency rows. The targets have no symbol, so every edge is INFERRED and
// file:line comes from the caller's own definition.
func graphInterfaceCallers(ix *index.Index, name string) []graphEdgeRow {
	rows := []graphEdgeRow{}
	for _, c := range ix.Callers[name] {
		row := graphEdgeRow{name: c, conf: "INFERRED"}
		if d, ok := ix.ResolveName(c); ok {
			row.loc = fmt.Sprintf(" — %s:%d", d.File, d.Line)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// graphInterfaceMethod reports whether name is "Receiver.Method" where
// Receiver is a locally-declared interface. Such names are call targets the
// index records without a definition — the dispatch is runtime-polymorphic.
func graphInterfaceMethod(ix *index.Index, name string) (recv, meth string, ok bool) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return "", "", false
	}
	recv, meth = name[:dot], name[dot+1:]
	s, found := ix.FindSymbol(recv)
	return recv, meth, found && s.Kind == "interface"
}

// graphDispatchImpls lists the concrete runtime targets of a call to an
// interface method: types in the same module that could satisfy the call.
// It merges explicit implements edges with Go-style structural satisfaction
// (any concrete type in the interface's package defining the method). Empty
// when name is not an interface method.
func graphDispatchImpls(ix *index.Index, name string) []string {
	recv, meth, ok := graphInterfaceMethod(ix, name)
	if !ok {
		return nil
	}
	recvSym, _ := ix.FindSymbol(recv)
	recvDir := filepath.Dir(recvSym.File)
	seen := map[string]bool{}
	out := []string{}
	add := func(t string) {
		if seen[t] || t == recv {
			return
		}
		seen[t] = true
		if graphHasMethod(ix, t, meth) {
			out = append(out, t+"."+meth)
		}
	}
	for _, t := range ix.SubtypesOf(recvSym) {
		add(t)
	}
	for _, s := range ix.Symbols {
		if s.Name != meth || s.Receiver == "" || s.Receiver == recv {
			continue
		}
		if filepath.Dir(s.File) != recvDir {
			continue
		}
		if t, found := ix.FindSymbol(s.Receiver); found && t.Kind != "interface" {
			add(s.Receiver)
		}
	}
	sort.Strings(out)
	return out
}

// graphHasMethod reports whether a type declares a method with the given
// name. Interface methods are not symbols, so this is exact for concrete
// types and false for interfaces.
func graphHasMethod(ix *index.Index, recv, meth string) bool {
	for _, s := range ix.Symbols {
		if s.Receiver == recv && s.Name == meth {
			return true
		}
	}
	return false
}

// graphDefNode returns the definition node for the root symbol.
func graphDefNode(g index.GraphResult, root string) *index.GraphNode {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == root && n.Role == "def" {
			return n
		}
	}
	return nil
}

// graphEdgeRow is one adjacency row: a neighbor name, its confidence label,
// and its file:line when a definition exists.
type graphEdgeRow struct {
	name string
	conf string // EXTRACTED / INFERRED / AMBIGUOUS
	loc  string // " — file:line" or ""
}

func (r graphEdgeRow) String() string {
	return r.name + " [" + r.conf + "]" + r.loc
}

// graphNeighbors returns the deduped, sorted adjacency of root in one
// direction ("caller" = from → root, "callee" = root → from), with each edge's
// confidence label and the neighbor's file:line when resolvable.
// Interface-method neighbors carry INFERRED and no file:line: the receiver has
// no definition, so a location would be misleading.
func graphNeighbors(ix *index.Index, g index.GraphResult, root, dir string) []graphEdgeRow {
	loc := map[string]string{}
	for _, n := range g.Nodes {
		if n.File != "" {
			loc[n.ID] = fmt.Sprintf(" — %s:%d", n.File, n.Line)
		}
	}
	rows := []graphEdgeRow{}
	seen := map[string]bool{}
	for _, e := range g.Edges {
		var nb string
		if dir == "caller" && e.To == root {
			nb = e.From
		} else if dir == "callee" && e.From == root {
			nb = e.To
		} else {
			continue
		}
		if seen[nb] {
			continue
		}
		seen[nb] = true
		conf := e.ConfidenceLabel
		if conf == "" {
			conf = e.Confidence
		}
		if conf == "" {
			conf = "AMBIGUOUS"
		}
		l := loc[nb]
		if _, _, isIface := graphInterfaceMethod(ix, nb); isIface {
			conf = "INFERRED"
			l = ""
		}
		rows = append(rows, graphEdgeRow{name: nb, conf: conf, loc: l})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// graphCommunityMembers returns the sorted member names of the community
// containing root, or nil when root participates in no local call edges. It
// prefers the persisted labels (ix.Communities) and falls back to computing
// label propagation on demand; both use the same index-package algorithm.
func graphCommunityMembers(ix *index.Index, root string) []string {
	label := ix.Communities
	if len(label) == 0 {
		label = ix.CommunityLabels()
	}
	groups := map[string][]string{}
	for n, l := range label {
		groups[l] = append(groups[l], n)
	}
	for _, members := range groups {
		found := false
		for _, m := range members {
			if m == root {
				found = true
				break
			}
		}
		if !found || len(members) < minCommunitySize {
			continue
		}
		sort.Strings(members)
		return members
	}
	return nil
}
