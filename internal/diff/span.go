package diff

import "github.com/JayveerPrajapati/kern/internal/index"

// IndexSpanResolver returns a SpanResolver over an index: it resolves
// file:line to the innermost symbol whose [Line, End] span contains the line
// (End is inclusive; symbols with End == 0 are skipped). A nil index yields a
// resolver that always returns "".
func IndexSpanResolver(ix *index.Index) SpanResolver {
	if ix == nil {
		return func(string, int) string { return "" }
	}
	// Group index symbols by file once per call; per-file slices are small, so
	// a linear scan over each is fine.
	byFile := make(map[string][]index.Symbol)
	for _, s := range ix.Symbols {
		if s.End > 0 && s.File != "" {
			byFile[s.File] = append(byFile[s.File], s)
		}
	}
	return func(file string, line int) string {
		for _, s := range byFile[file] {
			if line >= s.Line && line <= s.End {
				return s.FullName()
			}
		}
		return ""
	}
}
