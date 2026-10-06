package main

import (
	"fmt"
	"os"
)

// runProse implements `kern prose <words> [root] [--limit N]`: load-or-build
// the index, map prose words to candidate symbols via the build-time inverted
// vocab, and print one hit per line: `<symbol> (<N> words matched)`.
func runProse(rest []string) int {
	f, args := parseFlagsOrDie(rest)
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
	root := projectRoot(f)
	if len(args) > 1 {
		root = args[1]
	}
	// F13: `kern prose func run` (unquoted) used to split into words="func"
	// root="run" and then silently "succeed" (exit 0) on the nonexistent junk
	// root (0 matches, no hint that the words were misparsed). Reject excess
	// positionals and unresolvable root paths as usage errors (exit 2) — the
	// multi-word phrase must stay a single quoted argument
	// (`kern prose "save user"`).
	if len(args) > 2 {
		fatalUsage("usage: kern prose \"<words>\" [root] [--limit N]\n  too many arguments (words and optional root only); quote the phrase: kern prose \"save user\"")
	}
	if root != "" {
		if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
			fatalUsage("usage: kern prose \"<words>\" [root] [--limit N]\n  root %q does not exist or is not a directory (multi-word phrases must be quoted: kern prose \"save user\")", root)
		}
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
	// Audit L6: LookupProse caps at 20 when --limit is absent. When the
	// default cap may have bound, say so instead of truncating silently.
	if f.limit <= 0 && len(hits) >= 20 {
		fmt.Fprintln(os.Stderr, "kern: (showing first 20 lines; pass --limit to change)")
	}
	for _, h := range hits {
		fmt.Printf("%s (%d words matched)\n", h.Symbol, h.Matched)
	}
	return 0
}
