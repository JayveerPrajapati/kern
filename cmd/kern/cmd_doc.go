package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// runDocFetch fetches a public doc page into the local index + cache.
// Usage: kern doc_fetch <url> [--name N] [--root ROOT]
func runDocFetch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_fetch <url> [--name N] [--root ROOT]")
	}
	rawURL := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	res, err := fetch.Fetch(rawURL, 0)
	if err != nil {
		fatal("doc fetch: %v", err)
	}
	name := f.name
	if name == "" {
		name = slugName(rawURL)
	} else {
		name = strutil.Slug(name)
	}
	if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
		fatal("doc fetch: %v", err)
	}
	if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
		fatal("doc fetch: %v", err)
	}
	added, err := docsearch.MergeFetched(root, name, res.Text)
	if err != nil {
		fatal("doc fetch: %v", err)
	}
	if res.Truncated {
		fmt.Fprintf(os.Stderr, "warning: document exceeded limit, truncated to %d bytes\n", len(res.Text))
	}
	fmt.Printf("fetched %s (%d bytes, %d chunks indexed into %s doc index)\n", name, len(res.Text), added, root)
	if res.Title != "" {
		fmt.Printf("# %s\n\n", res.Title)
	}
	fmt.Println(clipText(res.Text, 600))
}

// runDocSearch performs a local vector search over the indexed documents.
// Usage: kern doc_search <query> [--root ROOT] [--limit N]
func runDocSearch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_search <query> [--root ROOT] [--limit N]")
	}
	query := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	k := f.limit
	if k <= 0 {
		k = 5
	}
	ix := docsearch.Load(root)
	if ix == nil {
		var err error
		ix, err = docsearch.IndexDir(root)
		if err != nil {
			fatal("doc search: %v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("doc search: %v", err)
		}
	}
	// If the persisted index carries dense vectors, re-attach the local
	// embedder so queries fuse the semantic signal too.
	if hasSemantic(ix) {
		client := llm.NewEmbedder()
		if client.HasEmbeddingModel() {
			docsearch.SemanticEmbedder = client
		}
	}
	results := ix.Search(query, k)

	var codeMatches []index.Symbol
	if len(results) == 0 || (len(results) > 0 && results[0].Sim <= 1.0) {
		if codeIx, err := loadOrBuild(root); err == nil && codeIx != nil {
			codeMatches = intel.RankedSearch(codeIx, query, k)
		}
	}

	if len(results) == 0 && len(codeMatches) == 0 {
		fmt.Println("no matching document fragments")
		return
	}

	if len(results) > 0 && len(codeMatches) > 0 {
		fmt.Println("## Documentation")
		for i, r := range results {
			fmt.Printf("#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
			if len(body) > 300 {
				body = body[:300] + "…"
			}
			fmt.Printf("  %s\n", body)
		}
		fmt.Println("\n## Code")
		for _, m := range codeMatches {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}
		return
	}

	if len(results) > 0 {
		for i, r := range results {
			fmt.Printf("#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
			if len(body) > 300 {
				body = body[:300] + "…"
			}
			fmt.Printf("  %s\n", body)
		}
		return
	}

	if len(codeMatches) > 0 {
		fmt.Println("## Code")
		for _, m := range codeMatches {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}
	}
}
