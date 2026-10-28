package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/astrefs"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/refscan"
)

// runRefs implements `kern refs <symbol>`: the reference set behind a symbol,
// AST-verified for Go (type/const/var/field uses the call graph cannot see)
// and falling back to the bounded textual safety net for other languages.
func runRefs(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fatalUsage("usage: kern refs <symbol> [root] [--json] [--root DIR]")
	}
	symbol := strings.TrimSpace(args[0])
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		fatal("Refs: %v", err)
	}
	d, ok := ix.ResolveName(symbol)
	if !ok {
		fmt.Fprintf(os.Stderr, "kern: no symbol found: %s\n", symbol)
		return 1
	}
	def := map[string]any{"file": d.File, "line": d.Line}
	if refs, complete := astrefs.References(ix, d); len(refs) > 0 {
		confidence := "ast"
		if !complete {
			confidence = "ast (scan truncated)"
		}
		files := astrefs.DistinctFiles(refs)
		if f.json {
			printJSON(map[string]any{
				"symbol": d.FullName(), "kind": d.Kind, "definition": def,
				"confidence": confidence, "count": len(refs),
				"files": files, "references": refs,
			})
			return 0
		}
		fmt.Printf("# %s %s -> %s:%d\n", d.Kind, d.FullName(), d.File, d.Line)
		fmt.Printf("references: %d in %d file(s) (%s)\n", len(refs), len(files), confidence)
		for _, r := range refs {
			fmt.Printf("%s:%d:%d  %s\n", r.File, r.Line, r.Col, r.Kind)
		}
		return 0
	}
	files, count, complete := refscan.TextualReferencesIncludingDecl(ix, d.Name, d.File, d.Line)
	confidence := "textual"
	if !complete {
		confidence = "textual (scan truncated)"
	}
	if f.json {
		printJSON(map[string]any{
			"symbol": d.FullName(), "kind": d.Kind, "definition": def,
			"confidence": confidence, "count": count, "files": files,
			"references": []astrefs.Reference{},
		})
		return 0
	}
	fmt.Printf("# %s %s -> %s:%d\n", d.Kind, d.FullName(), d.File, d.Line)
	fmt.Printf("references: %d in %d file(s) (%s; no line positions)\n", count, len(files), confidence)
	for _, file := range files {
		fmt.Println(file)
	}
	return 0
}
