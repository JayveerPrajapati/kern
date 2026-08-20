package intel

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// DeadSymbol is a symbol that nothing in the project calls. Public (exported)
// names are flagged separately because they may be external API; private names
// with no callers are dead in the strict sense.
type DeadSymbol struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Lines  int    `json:"lines"`
	Public bool   `json:"public"`
}

// DeadCode finds callable symbols (functions and methods) with no in-project
// callers. Types, vars and consts are skipped — the call graph only tracks
// function calls. Symbols called only from tests and symbols with zero callers
// anywhere are reported. Results are sorted by size (largest first).
//
// A symbol referenced outside a call position (a function/method value, an
// interface member, an argument) is not reported dead: the call graph alone
// cannot see it, but it is clearly still in use.
//
// Known limitation: interface dispatch is still not fully modelled. A method
// that satisfies an interface and is invoked only through that interface may be
// reported dead even though it is in use.
func DeadCode(ix *index.Index) []DeadSymbol {
	fileMap := buildFileMap(ix)
	// Scan the indexed Go files once for non-call references. Hoisting this out
	// of the loop keeps DeadCode linear in the number of files.
	refs := newNonCallRefIndex(ix)
	var out []DeadSymbol
	for _, s := range ix.Symbols {
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		full := s.FullName()
		if isTestFile(s.File) || isEntryPoint(full) {
			continue
		}
		// A bare-identifier reference outside a call position means the symbol
		// is in use even with no callers (e.g. `f := Foo; f()` or a method
		// value). Don't list it as dead.
		if refs.referencedOutsideCall(simpleName(full)) {
			continue
		}
		callers := ix.CallersFor(s)
		if len(callers) == 0 {
			out = append(out, DeadSymbol{
				Name: full, Kind: s.Kind, File: s.File, Line: s.Line,
				Lines: s.Lines(), Public: isPublic(s.Name),
			})
			continue
		}
		// Called only from tests: production-dead, test-alive.
		testOnly := true
		for _, c := range callers {
			if f := fileMap[c]; f == "" || !isTestFile(f) {
				testOnly = false
				break
			}
		}
		if testOnly {
			out = append(out, DeadSymbol{
				Name: full, Kind: s.Kind, File: s.File, Line: s.Line,
				Lines: s.Lines(), Public: isPublic(s.Name),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// isPublic reports whether a name follows the exported-name convention
// (leading uppercase, matching Go and most languages' export rules).
func isPublic(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return unicode.IsUpper(r)
}

// RenderDead returns a compact dead-code report. Counts are shown at the end
// so the report is immediately actionable.
func RenderDead(dead []DeadSymbol) string {
	var b strings.Builder
	b.WriteString("dead code (no in-project callers):\n")
	public, private := 0, 0
	for _, d := range dead {
		tag := ""
		if d.Public {
			tag = "  [public - may be external API]"
			public++
		} else {
			private++
		}
		fmt.Fprintf(&b, "  %-8s %-32s %s:%d  (%d lines)%s\n",
			d.Kind, d.Name, d.File, d.Line, d.Lines, tag)
	}
	if len(dead) == 0 {
		b.WriteString("  (none — every non-entry symbol has a caller)\n")
	}
	fmt.Fprintf(&b, "summary: %d dead symbols (%d private, %d public-API)\n",
		len(dead), private, public)
	return strings.TrimSuffix(b.String(), "\n")
}
