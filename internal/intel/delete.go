package intel

import (
	"sort"
	"strings"
	"unicode"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// DeleteReport summarises whether a symbol can be safely deleted. "Safe" is
// conservative: it means no in-project call edges outside tests, the symbol is
// not an entry point, and it is not exported (so external/dynamic callers are
// unlikely). A symbol referenced only from test files is reported Safe because
// removing the tests alongside the symbol is part of the same change.
type DeleteReport struct {
	Symbol      string   `json:"symbol"`
	Defined     bool     `json:"defined"`
	File        string   `json:"file,omitempty"`
	Defs        int      `json:"defs"`
	Exported    bool     `json:"exported"`
	EntryPoint  bool     `json:"entry_point"`
	Callers     []string `json:"callers"`
	TestCallers []string `json:"test_callers"`
	Safe        bool     `json:"safe"`
	Reason      string   `json:"reason,omitempty"`
}

// DeleteCheck analyses whether sym can be removed. Callers come from the
// index's call edges (ix.Callers); test callers are split out so deleting a
// symbol together with its tests is not flagged as unsafe. The check cannot
// see dynamic references (reflection, string-built calls) or usage outside the
// indexed project, so an exported symbol is always reported as unsafe.
func DeleteCheck(ix *index.Index, sym string) DeleteReport {
	r := DeleteReport{Symbol: sym}
	if ix == nil || sym == "" {
		r.Reason = "no index or empty symbol"
		return r
	}
	defs := ix.Search(sym, 50)
	for _, d := range defs {
		if d.FullName() == sym || d.Name == sym {
			r.Defined = true
			r.Defs++
			if r.File == "" {
				r.File = d.File
			}
		}
	}
	if !r.Defined {
		r.Reason = "symbol not found in the index (run kern index first, or the name may be qualified differently)"
		return r
	}

	// Exportedness is determined by the final segment (method name for T.M,
	// symbol for pkg.Symbol), not the receiver/package qualifier: a lowercase
	// receiver with an exported method is still an exported symbol.
	seg := sym
	if i := strings.LastIndexByte(sym, '.'); i >= 0 {
		seg = sym[i+1:]
	}
	if len(seg) > 0 && unicode.IsUpper(rune(seg[0])) {
		r.Exported = true
	}
	r.EntryPoint = isEntryPoint(sym)

	fileMap := buildFileMap(ix)
	for _, c := range ix.Callers[sym] {
		if f := fileMap[c]; f == "" || !isTestFile(f) {
			r.Callers = append(r.Callers, c)
		} else {
			r.TestCallers = append(r.TestCallers, c)
		}
	}
	sort.Strings(r.Callers)
	sort.Strings(r.TestCallers)

	switch {
	case len(r.Callers) > 0:
		r.Reason = "called from production code: " + strings.Join(r.Callers, ", ")
	case r.EntryPoint:
		r.Reason = "entry point (" + sym + "): removing it breaks startup/CLI wiring"
	case r.Exported:
		r.Reason = "exported symbol: external or dynamic callers are invisible to the index"
	case len(r.TestCallers) > 0:
		r.Reason = "only referenced from tests (" + strings.Join(r.TestCallers, ", ") + "); remove the tests together"
		r.Safe = true
	default:
		r.Reason = "no in-project callers; not exported; not an entry point"
		r.Safe = true
	}
	return r
}

// RenderDelete formats a DeleteReport for terminal/MCP output.
func RenderDelete(r DeleteReport) string {
	var b strings.Builder
	if r.Defs > 1 {
		b.WriteString("warning: symbol defined " + itoa(r.Defs) + " times (first at " + r.File + ")\n")
	} else if r.Defined {
		b.WriteString("defined at " + r.File + "\n")
	}
	if r.Safe {
		b.WriteString("SAFE to delete: " + r.Reason + "\n")
	} else {
		b.WriteString("NOT SAFE: " + r.Reason + "\n")
	}
	if r.Exported {
		b.WriteString("exported: true\n")
	}
	if r.EntryPoint {
		b.WriteString("entry point: true\n")
	}
	if len(r.Callers) > 0 {
		b.WriteString("production callers:\n")
		for _, c := range r.Callers {
			b.WriteString("  " + c + "\n")
		}
	}
	if len(r.TestCallers) > 0 {
		b.WriteString("test-only callers:\n")
		for _, c := range r.TestCallers {
			b.WriteString("  " + c + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
