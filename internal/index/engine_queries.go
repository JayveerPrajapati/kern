package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/filecache"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/tokstats"
)

// SupertypesOf returns the inheritance/implementation bases of a symbol as
// tagged edges ("extends:Animal", "implements:Pet", "embeds:Base"), deduped
// under any key form of s.
func (ix *Index) SupertypesOf(s Symbol) []string {
	var out []string
	for _, k := range edgeKeys(s) {
		out = append(out, ix.Inherits[k]...)
	}
	return dedupeSorted(out)
}

// SubtypesOf returns the symbols that extend/implement a base, deduped under
// any key form of the base.
func (ix *Index) SubtypesOf(s Symbol) []string {
	var out []string
	for _, k := range edgeKeys(s) {
		out = append(out, ix.InheritedBy[k]...)
	}
	return dedupeSorted(out)
}

// Graph renders the neighbourhood of a symbol: definition, callers, and what
// it calls.
func (ix *Index) Graph(symbol string) string {
	var b strings.Builder
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			b.WriteString("no symbol found: " + symbol)
			return b.String()
		}
	}
	root := defs[0]
	// The graph text names definitions, callers and callees across several
	// files; the savings denominator must cover ALL of those node sources
	// (finding V1), so collect the distinct files backing the rendered nodes
	// — the definitions' own files plus each resolved caller/callee's file.
	nodeFiles := map[string]bool{}
	for _, d := range defs {
		if d.File != "" {
			nodeFiles[d.File] = true
		}
	}
	for _, d := range defs {
		b.WriteString("def ")
		b.WriteString(d.Kind)
		b.WriteString(" ")
		b.WriteString(d.FullName())
		b.WriteString(" ")
		b.WriteString(d.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(d.Line))
		b.WriteString("\n")
	}
	callers := ix.CallersFor(root)
	if len(callers) > 0 {
		b.WriteString("callers:\n")
		for _, c := range callers {
			b.WriteString("  ")
			b.WriteString(c)
			if d, ok := resolveName(ix, c); ok {
				if d.File != "" {
					nodeFiles[d.File] = true
				}
			} else {
				b.WriteString("  (unresolved)")
			}
			b.WriteString("\n")
		}
	}
	callees := ix.CallsFor(root)
	if len(callees) > 0 {
		b.WriteString("calls:\n")
		for _, c := range callees {
			b.WriteString("  ")
			b.WriteString(c)
			if d, ok := resolveName(ix, c); ok {
				if d.File != "" {
					nodeFiles[d.File] = true
				}
			} else {
				b.WriteString("  (unresolved)")
			}
			b.WriteString("\n")
		}
	}
	result := strings.TrimSuffix(b.String(), "\n")
	files := make([]string, 0, len(nodeFiles))
	for f := range nodeFiles {
		files = append(files, f)
	}
	slices.Sort(files)
	stats := tokstats.TokenSavingsForGraph(ix.Root, files, result)
	if summary := stats.Summary(); summary != "" {
		result += "\n\n" + summary
	}
	return result
}

// Context returns the minimal slice of source an agent needs about a symbol:
// its definition source, its callers, and what it calls.
func (ix *Index) Context(symbol string, linesAround int) string {
	if linesAround <= 0 {
		linesAround = 12
	}
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			return ""
		}
	}
	return ix.ContextDef(defs[0], linesAround)
}

// ContextDef slices source for an EXPLICIT symbol — definition window plus
// callers/callees — without re-resolving the name. Context(symbol, n)
// re-resolves a bare name with a different first-match order than the
// caller's own resolution, which could mix candidates (e.g. a TS interface
// definition with a Go method source window for the same bare name).
func (ix *Index) ContextDef(d Symbol, linesAround int) string {
	if linesAround <= 0 {
		linesAround = 12
	}
	var b strings.Builder
	abs := filepath.Join(ix.Root, d.File)
	// F-3: serve the line window from the shared file-content cache when the
	// file is unchanged (size:mtime re-checked on every Get); read the whole
	// file as before only on a miss. The stat is taken once and passed into
	// the cache so the stored entry is tagged with the version the bytes were
	// read under. Credential-shaped files are never cached (same guard
	// kern_compact_file applies, internal/mcp/context/context.go).
	fi, err := os.Stat(abs)
	if err != nil {
		return ""
	}
	var all []string
	if cached, ok := filecache.Default.Get(abs, fi); ok {
		all = strings.Split(string(cached), "\n")
	} else {
		src, err := os.ReadFile(abs)
		if err != nil {
			return ""
		}
		if !fsutil.LooksLikeCredentialFile(abs) {
			filecache.Default.Put(abs, src, fi)
		}
		all = strings.Split(string(src), "\n")
	}
	start := d.Line - linesAround
	if start < 1 {
		start = 1
	}
	// Function-aware extent (CG-P0-3): every extractor records the symbol's
	// syntactic end line (go/ast, tree-sitter node ranges, regex brace
	// heuristics), so the window extends at least to the definition's end —
	// a truncated body is never served when the full one is known.
	end := d.Line + linesAround
	if d.End > d.Line && d.End > end {
		end = d.End
	}
	if end > len(all) {
		end = len(all)
	}
	for i := start; i <= end; i++ {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(": ")
		b.WriteString(all[i-1])
		b.WriteString("\n")
	}
	callers := ix.CallersFor(d)
	if len(callers) > 0 {
		b.WriteString("\ncallers: ")
		b.WriteString(strings.Join(contextCallerLabels(ix, callers), ", "))
		b.WriteString("\n")
	}
	if callees := ix.CallsFor(d); len(callees) > 0 {
		b.WriteString("calls: ")
		b.WriteString(strings.Join(callees, ", "))
		b.WriteString("\n")
	}
	result := strings.TrimSuffix(b.String(), "\n")
	stats := tokstats.TokenSavingsForContext(ix.Root, d.File, result)
	if summary := stats.Summary(); summary != "" {
		result += "\n\n" + summary
	}
	return result
}

// maxContextCallers caps the callers line of a context slice. Context is a
// token-budget tool: a hub with hundreds of callers must not blow the budget
// on one line, so the row list stops at maxContextCallers and the remainder
// is summarized as a count.
const maxContextCallers = 10

// contextCallerLabels renders each caller as "name (file:line)" when its
// definition resolves against the index symbol map, capped at
// maxContextCallers rows (the remainder collapses to "… and N more"). A
// caller whose definition cannot be resolved (cross-package qualified ref,
// nodesForIDs limitation) keeps its bare name — never fabricated.
func contextCallerLabels(ix *Index, callers []string) []string {
	shown := callers
	if len(shown) > maxContextCallers {
		shown = shown[:maxContextCallers]
	}
	out := make([]string, 0, len(shown)+1)
	for _, c := range shown {
		label := c
		if d, ok := ix.ResolveName(c); ok && d.File != "" && !ambiguousDef(ix, c) {
			label = fmt.Sprintf("%s (%s:%d)", c, d.File, d.Line)
		}
		out = append(out, label)
	}
	if len(callers) > maxContextCallers {
		out = append(out, fmt.Sprintf("… and %d more", len(callers)-maxContextCallers))
	}
	return out
}
