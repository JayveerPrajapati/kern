package architecture

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Report is the outcome of a validation run.
type Report struct {
	OK           bool
	Violations   []Violation
	WarningCount int
	ErrorCount   int
}

// ValidateProject loads the config and runs Check over all source files. A
// missing config yields an empty (passing) report; a malformed file fails closed.
func ValidateProject(root string) (*Report, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	return buildReport(NewEngine(cfg).Check(ix, sourceFiles(ix))), nil
}

// ValidateProjectWithIndex is like ValidateProject but runs against a prebuilt
// index the caller owns (treat it as read-only).
func ValidateProjectWithIndex(root string, ix *index.Index) (*Report, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	return buildReport(NewEngine(cfg).Check(ix, sourceFiles(ix))), nil
}

// ValidateDiff validates only the files that changed in a diff.
// files = relative paths of changed files. Returns a report scoped to them.
func ValidateDiff(root string, files []string) (*Report, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	return buildReport(NewEngine(cfg).Check(ix, files)), nil
}

// ValidateDiffWithIndex is like ValidateDiff but runs against a prebuilt index
// the caller owns and must treat as read-only.
func ValidateDiffWithIndex(root string, ix *index.Index, files []string) (*Report, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	return buildReport(NewEngine(cfg).Check(ix, files)), nil
}

// Render returns a human-readable validation report: the base violation lines
// from intel.RenderViolations, then a summary line "N errors, M warnings".
func Render(r *Report) string {
	var b strings.Builder
	if r == nil {
		return intel.RenderViolations(nil) + "\n0 errors, 0 warnings"
	}
	b.WriteString(intel.RenderViolations(toIntel(r.Violations)))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%d errors, %d warnings", r.ErrorCount, r.WarningCount)
	return b.String()
}

// buildReport folds a violation slice into a Report, counting errors/warnings.
func buildReport(vs []Violation) *Report {
	errCount, warnCount := 0, 0
	for _, v := range vs {
		if v.Severity == "warning" {
			warnCount++
		} else {
			errCount++
		}
	}
	return &Report{
		OK:           errCount == 0,
		Violations:   vs,
		ErrorCount:   errCount,
		WarningCount: warnCount,
	}
}

// sourceFiles returns the sorted set of files that carry indexed symbols.
func sourceFiles(ix *index.Index) []string {
	set := map[string]bool{}
	for f := range ix.SymbolsByFile {
		set[f] = true
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// toIntel flattens architecture violations into the intel shape for rendering.
func toIntel(vs []Violation) []intel.Violation {
	out := make([]intel.Violation, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Violation)
	}
	return out
}
