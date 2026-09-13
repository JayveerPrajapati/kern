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
		fatalUsage("usage: kern prose <words> [root] [--limit N]")
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
