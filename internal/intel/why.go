package intel

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// CallerRef is one caller of a symbol plus a one-line rationale and the
// provenance label of the caller→symbol edge
// (EXTRACTED/INFERRED/AMBIGUOUS).
type CallerRef struct {
	Name       string `json:"name"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	Confidence string `json:"confidence,omitempty"` // EXTRACTED/INFERRED/AMBIGUOUS
}

// WhyInfo is the rationale and doc-reference report for a symbol.
type WhyInfo struct {
	Symbol  index.Symbol `json:"symbol"`
	Doc     string       `json:"doc,omitempty"`
	Callers []CallerRef  `json:"callers,omitempty"`
	Callees int          `json:"callees"`
	InEdges int          `json:"incoming_edges"`
}

// Why builds a rationale report for a symbol: what it is (kind + doc comment),
// who depends on it and why, and how many things it depends on. Callers without
// a resolvable definition are still listed; their rationale falls back to the
// call-site context if one is documented.
func Why(ix *index.Index, symbol string) (WhyInfo, bool) {
	d, ok := ix.ResolveName(symbol)
	if !ok {
		return WhyInfo{}, false
	}
	info := WhyInfo{
		Symbol: d,
		Doc:    docComment(ix.Root, d.File, d.Line),
	}
	callers := ix.CallersFor(d)
	for _, c := range callers {
		ref := CallerRef{Name: c, Confidence: EdgeConfidenceLabel(ix, c, d.FullName())}
		if def, ok := ix.ResolveName(c); ok {
			ref.File, ref.Line = def.File, def.Line
			ref.Rationale = firstDocLine(ix.Root, def.File, def.Line)
		} else {
			ref.Rationale = "(unresolved caller)"
		}
		info.Callers = append(info.Callers, ref)
	}
	info.InEdges = len(callers)
	info.Callees = len(ix.CallsFor(d))
	return info, true
}

// docComment returns the contiguous comment block directly above a symbol's
// definition line (the doc reference), empty if none. Line comments are
// collected verbatim; block comments have their /* */ markers and leading "*"
// decoration stripped.
func docComment(root, file string, line int) string {
	doc := sourceLines(root, file, line-30, line-1)
	if len(doc) == 0 {
		return ""
	}
	var comment []string
	inBlock := false
	for i := len(doc) - 1; i >= 0; i-- {
		t := strings.TrimSpace(doc[i])
		if strings.HasSuffix(t, "*/") {
			// closing line of a block comment: strip the marker and continue
			// scanning upward for the opening line.
			inBlock = true
			comment = append(comment, cleanBlockLine(strings.TrimSuffix(t, "*/")))
			if strings.HasPrefix(t, "/*") {
				// single-line block comment: /* foo */ is its own opening and
				// closing line, so the scan must not overrun upward.
				break
			}
			continue
		}
		if inBlock {
			if strings.HasPrefix(t, "/*") {
				comment = append(comment, cleanBlockLine(strings.TrimPrefix(t, "/*")))
				break
			}
			comment = append(comment, cleanBlockLine(t))
			continue
		}
		if t == "" {
			break // blank line ends the doc
		}
		if strings.HasPrefix(t, "//") {
			comment = append(comment, strings.TrimSpace(strings.TrimPrefix(t, "//")))
			continue
		}
		break // non-comment line above
	}
	// reverse back to source order (top comment line first)
	for i, j := 0, len(comment)-1; i < j; i, j = i+1, j-1 {
		comment[i], comment[j] = comment[j], comment[i]
	}
	return strings.TrimSpace(strings.Join(comment, "\n"))
}

// cleanBlockLine strips the /* */ markers and leading "*" decoration from a
// block comment line.
func cleanBlockLine(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "/*")
	t = strings.TrimSuffix(t, "*/")
	t = strings.TrimPrefix(t, "*")
	t = strings.TrimPrefix(t, "*")
	return strings.TrimSpace(t)
}

// firstDocLine returns the first line of a symbol's doc comment (a one-line
// rationale), or the definition's first line if it has no comment.
func firstDocLine(root, file string, line int) string {
	doc := docComment(root, file, line)
	if doc != "" {
		if i := strings.IndexByte(doc, '\n'); i >= 0 {
			return doc[:i]
		}
		return doc
	}
	return "(no doc)"
}

// sourceLines returns up to n source lines ending at line (1-based). Empty if
// the file cannot be read.
func sourceLines(root, file string, from, to int) []string {
	path := filepath.Join(root, file)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if from < 1 {
		from = 1
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	i := 1
	for sc.Scan() {
		if i > to {
			break
		}
		if i >= from {
			out = append(out, sc.Text())
		}
		i++
	}
	return out
}

// FormatWhy renders the why report for the terminal.
func FormatWhy(info WhyInfo) string {
	return formatWhy(info, "", 0)
}

// FormatWhyBudgeted is formatWhy for the budgeted-explore surface
// (internal/explorebudget): source is the report's source block (the doc
// comment dedups against it) and maxTok bounds the caller list with a
// "+N more (raise max_tokens)" marker.
func FormatWhyBudgeted(info WhyInfo, source string, maxTok int) string {
	return formatWhy(info, source, maxTok)
}

// formatWhy is the shared why renderer. source is the report's source block:
// when it already shows the symbol's doc comment verbatim (the == source ==
// block of kern_explore --explain), the doc is referenced instead of repeated
// so it renders exactly once (QA 2026-10-19 item 1); an empty source keeps
// the full doc (standalone kern why). maxTok > 0 bounds the caller list with
// a "+N more (raise max_tokens)" marker, dropping the whole section when even
// its header cannot fit (QA 2026-10-19 item 2).
func formatWhy(info WhyInfo, source string, maxTok int) string {
	s := info.Symbol
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  %s:%d\n", s.Kind, s.FullName(), s.File, s.Line)
	if info.Doc != "" {
		if source != "" && sourceShowsDoc(source, info.Doc) {
			// The doc comment is already visible in the report's source
			// block above — reference it, never repeat it.
			b.WriteString("\ndoc: see the == source == block above (rendered once)\n")
		} else {
			fmt.Fprintf(&b, "\ndoc:\n  %s\n", strings.ReplaceAll(info.Doc, "\n", "\n  "))
		}
	} else {
		b.WriteString("\ndoc: (none)\n")
	}
	fmt.Fprintf(&b, "\nincoming edges: %d, outgoing calls: %d\n", info.InEdges, info.Callees)
	if len(info.Callers) > 0 {
		b.WriteString("\nwho depends on it and why:\n")
		if maxTok <= 0 {
			for _, c := range info.Callers {
				b.WriteString(callerRow(c))
			}
		} else {
			// Measure the FULL list before deciding: the budgeted branch only
			// trims when the whole list cannot fit (QA 2026-10-19 item 2).
			core := tokenize.Count(b.String())
			rows := make([]string, len(info.Callers))
			toks := make([]int, len(info.Callers))
			total := 0
			for i, c := range info.Callers {
				rows[i] = callerRow(c)
				toks[i] = tokenize.Count(rows[i])
				total += toks[i]
			}
			if core+total <= maxTok {
				for _, r := range rows {
					b.WriteString(r)
				}
			} else {
				room := maxTok - core
				kept := 0
				for i := range rows {
					rest := len(rows) - i - 1
					m := ""
					if rest > 0 {
						m = fmt.Sprintf("+%d more (raise max_tokens)\n", rest)
					}
					if toks[i]+tokenize.Count(m) > room {
						break
					}
					b.WriteString(rows[i])
					kept++
					room -= toks[i]
				}
				if kept < len(rows) {
					fmt.Fprintf(&b, "+%d more (raise max_tokens)\n", len(rows)-kept)
				}
			}
		}
	} else {
		b.WriteString("\nno callers in the index\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// callerRow renders one "who depends on it and why" row.
func callerRow(c CallerRef) string {
	tag := ""
	if c.Confidence != "" {
		tag = " [" + c.Confidence + "]"
	}
	return fmt.Sprintf("  %-28s %s:%d%s  %s\n", c.Name, c.File, c.Line, tag, c.Rationale)
}

// sourceShowsDoc reports whether the source block already contains the doc
// comment verbatim: the first two doc lines (with or without their "//"
// prefix — the source block is line-numbered) must both appear. The
// two-line check keeps a coincidental short first-line match from wrongly
// suppressing a doc that the token fitting folded out of the source.
func sourceShowsDoc(source, doc string) bool {
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || lines[0] == "" {
		return false
	}
	hit := func(l string) bool {
		return strings.Contains(source, l) || strings.Contains(source, "// "+l)
	}
	if !hit(lines[0]) {
		return false
	}
	if len(lines) == 1 {
		return true
	}
	return hit(lines[1])
}
