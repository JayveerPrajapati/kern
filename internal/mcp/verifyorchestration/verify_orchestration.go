package verifyorchestration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// FailedTestEvidence maps go test failure names to indexed declarations. It
// keeps the output one hop from the failure to its source anchor, without
// changing the governed verification command or its approval path.
func FailedTestEvidence(ix *index.Index, v verdict.VerificationResult) string {
	var names []string
	seen := map[string]bool{}
	for _, result := range []*verdict.TestResult{v.UnitTests, v.Integration} {
		if result == nil {
			continue
		}
		for _, name := range verdict.FailedTestNames(result.Output) {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	const maxNames = 20
	lines := []string{"failed test evidence:"}
	shown := names
	if len(shown) > maxNames {
		shown = shown[:maxNames]
	}
	for _, name := range shown {
		lines = append(lines, name+" -> "+failedTestAnchor(ix, name))
	}
	if len(names) > maxNames {
		lines = append(lines, fmt.Sprintf("... %d more failed tests", len(names)-maxNames))
	}
	return strings.Join(lines, "\n")
}

func failedTestAnchor(ix *index.Index, name string) string {
	if ix == nil {
		return "not indexed"
	}
	symbols := ix.Search(name, 100)
	tests := make([]index.Symbol, 0, len(symbols))
	for _, symbol := range symbols {
		if strings.HasSuffix(symbol.File, "_test.go") {
			tests = append(tests, symbol)
		}
	}
	if len(tests) == 0 {
		tests = symbols
	}
	switch len(tests) {
	case 0:
		return "not indexed"
	case 1:
		return failedTestSymbol(ix, tests[0])
	default:
		anchors := make([]string, 0, len(tests))
		for _, symbol := range tests {
			anchors = append(anchors, failedTestSymbol(ix, symbol))
		}
		return "ambiguous (" + strings.Join(anchors, "; ") + ")"
	}
}

func failedTestSymbol(ix *index.Index, symbol index.Symbol) string {
	qualified := symbol.FullName()
	if pkg := ix.Pkgs[filepath.ToSlash(filepath.Dir(symbol.File))]; pkg != nil && pkg.Path != "" {
		qualified = pkg.Path + "." + qualified
	}
	return fmt.Sprintf("%s — %s:%d", qualified, symbol.File, symbol.Line)
}
