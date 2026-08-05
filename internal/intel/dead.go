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
// callers. Types, vars and consts are skipped: the call graph only tracks
// function calls, so they would all look uncalled. Symbols only referenced
// from test files are reported with the test-only flag; symbols with zero
// callers anywhere are the strongest dead-code candidates. Results are sorted
// by size (largest dead code first) so the biggest cleanup wins show up top.
func DeadCode(ix *index.Index) []DeadSymbol {
	fileMap := buildFileMap(ix)
	var out []DeadSymbol
	for _, s := range ix.Symbols {
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		full := s.FullName()
		if isTestFile(s.File) || isEntryPoint(s.Name) {
			continue
		}
		callers := ix.Callers[full]
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
