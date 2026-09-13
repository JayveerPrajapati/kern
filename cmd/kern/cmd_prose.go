package main

import (
	"fmt"
)

// runProse implements `kern prose <words> [root] [--limit N]`: load-or-build
// the index, map prose words to candidate symbols via the build-time inverted
// vocab, and print one hit per line: `<symbol> (<N> words matched)`.
func runProse(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("prose: missing <words> argument\n\n" +
			"usage: kern prose \"<words>\" [root] [--limit N]\n\n" +
			"<words> is a natural-language phrase (in quotes) that is mapped, via the\n" +
			"build-time inverted vocab, to the symbol candidates whose tokens match\n" +
			"best. One hit is printed per line as `<symbol> (<N> words matched)`.\n\n" +
			"examples:\n" +
			"  kern prose \"save user\"          # find symbols about saving users\n" +
			"  kern prose \"dispatch request\" --limit 5\n" +
			"  kern prose \"token budget\" ./some/repo")
	}
	query := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Prose: %v", err)
	}
	hits := ix.LookupProse(query, f.limit)
	if len(hits) == 0 {
		fmt.Printf("no prose matches: %s\n", query)
		return 0
	}
	for _, h := range hits {
		fmt.Printf("%s (%d words matched)\n", h.Symbol, h.Matched)
	}
	return 0
}
