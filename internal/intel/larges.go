package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// LargeSymbol is a declaration that exceeds a size threshold in source lines.
type LargeSymbol struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Lines int    `json:"lines"`
}

// LargeFunctions returns the largest function/method-like declarations in the
// index, sorted by size descending. minLines is the exclusive floor (size >=
// minLines qualifies); only function/method kinds are considered.
func LargeFunctions(ix *index.Index, minLines int) []LargeSymbol {
	var out []LargeSymbol
	for _, s := range ix.Symbols {
		if isTestFile(s.File) {
			continue
		}
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		if n := s.Lines(); n >= minLines {
			out = append(out, LargeSymbol{Name: s.FullName(), Kind: s.Kind, File: s.File, Line: s.Line, Lines: n})
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

// RenderLarge returns a compact report of oversized declarations.
func RenderLarge(large []LargeSymbol) string {
	var b strings.Builder
	if len(large) == 0 {
		return "no declarations exceed the size threshold"
	}
	b.WriteString("largest declarations (size in source lines):\n")
	for _, l := range large {
		fmt.Fprintf(&b, "  %-8s %-40s %s:%d  (%d lines)\n",
			l.Kind, l.Name, l.File, l.Line, l.Lines)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
