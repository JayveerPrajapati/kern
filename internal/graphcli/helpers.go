package graphcli

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/clihints"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// loadOrBuild delegates to index.LoadOrBuild — the canonical shared
// implementation. Kept as a thin wrapper for its many CLI callers.
func loadOrBuild(root string) (*index.Index, error) {
	return index.LoadOrBuild(root)
}

// suggestSymbols returns up to 5 symbol names from ix that are similar to
// query — the local alias for the shared clihints.Suggest (consolidation
// tranche 8), kept so the many graph-family callers keep working unchanged.
func suggestSymbols(ix *index.Index, query string) []string {
	return clihints.Suggest(ix, query)
}

// fatalNoSymbol prints a "no symbol found" error with optional suggestions.
func fatalNoSymbol(symbol string, ix *index.Index) {
	fatal("%s", clihints.NoSymbolMsg(symbol, ix))
}

// fatalNoSearchMatch prints a "no symbols matched" error with optional
// suggestions (the same did-you-mean UX as fatalNoSymbol) and exits 1 — the
// `kern search` no-match contract (F1): search used to exit 0 on an empty
// result, breaking the explore/graph convention that "not found" is an error
// for scripts and CI. The --json path is untouched: an empty result array is
// data, not an error, and stays exit 0.
func fatalNoSearchMatch(query string, ix *index.Index) {
	fatal("%s", clihints.NoSearchMatchMsg(query, ix))
}

func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// resolveRoot resolves the project root shared by the graph subcommands — the
// --root flag, else the first positional argument, else "." — and opens the
// project index, reproducing the root-resolution + index-open preamble each
// subcommand repeats. The index-open error is returned so each caller formats
// its own failure message.
func resolveRoot(f flags, args []string) (string, *index.Index, error) {
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		return root, nil, err
	}
	return root, ix, nil
}

// projectRoot returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble most subcommands repeat (previously
// an inline "default root to ." guard at every call site).
func projectRoot(f flags) string {
	if f.root != "" {
		return f.root
	}
	return "."
}

// parseFlagsOrDie parses subcommand flags, exiting with a usage error (exit
// code 2) on failure — the flag-parsing preamble every subcommand repeats
// (previously an inline err-check + fatalUsage at every call site).
func parseFlagsOrDie(rest []string) (flags, []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}
